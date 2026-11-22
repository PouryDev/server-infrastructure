import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Environment } from "./Environment";

const state = {
  project: "shop",
  revision: "r".repeat(64),
  services: [
    { name: "api", oneShot: false, usesProjectEnv: true, hasOverride: false },
    { name: "shop-migrate", oneShot: true, usesProjectEnv: true, hasOverride: false },
  ],
  files: [
    {
      scope: "",
      exists: true,
      variables: [
        { name: "DB_PASSWORD", masked: true, sensitive: true, empty: false },
        { name: "APP_ENV", masked: true, sensitive: false, empty: false },
      ],
    },
  ],
};
const response = (body: unknown) => ({ ok: true, status: 200, json: async () => body });
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("masks every value, reveals one on request and saves reviewed edits with revision and CSRF", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    calls.push({ path, init });
    if (path === "/api/env/projects") return response([{ name: "shop", services: ["api"] }]);
    if (path === "/api/env/shop/reveal") return response({ value: "hunter2-value" });
    if (path === "/api/env/shop" && init?.method === "PUT") return response(state);
    if (path === "/api/env/shop") return response(state);
    throw new Error(path);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<Environment csrf="csrf-token" onUnauthorized={vi.fn()} />);
  const user = userEvent.setup();
  expect(await screen.findByText("DB_PASSWORD")).toBeTruthy();
  expect(screen.queryByText("hunter2-value")).toBeNull();
  expect(screen.queryByText("production")).toBeNull();
  await user.click(screen.getByRole("button", { name: "نمایش DB_PASSWORD" }));
  expect(await screen.findByText("hunter2-value")).toBeTruthy();
  const reveal = calls.find((c) => c.path.endsWith("/reveal"))!;
  expect((reveal.init?.headers as Record<string, string>)["X-CSRF-Token"]).toBe("csrf-token");
  expect(JSON.parse(reveal.init!.body as string)).toEqual({ scope: "", name: "DB_PASSWORD" });

  await user.type(screen.getByLabelText("نام متغیر"), "NEW_VAR");
  await user.type(screen.getByLabelText("مقدار متغیر"), "new-value");
  await user.click(screen.getByRole("button", { name: /افزودن یا ویرایش/ }));
  await user.click(screen.getByRole("button", { name: "حذف APP_ENV" }));
  await user.click(screen.getByRole("button", { name: /ذخیره در مقصد \(/ }));
  await waitFor(() => expect(calls.some((c) => c.init?.method === "PUT")).toBe(true));
  const put = calls.find((c) => c.init?.method === "PUT")!;
  expect((put.init?.headers as Record<string, string>)["X-CSRF-Token"]).toBe("csrf-token");
  const body = JSON.parse(put.init!.body as string);
  expect(body.revision).toBe("r".repeat(64));
  expect(body.changes).toEqual([
    { scope: "", name: "NEW_VAR", action: "set", value: "new-value" },
    { scope: "", name: "APP_ENV", action: "delete" },
  ]);
});

it("rejects invalid variable names before any request is sent", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/api/env/projects") return response([{ name: "shop", services: ["api"] }]);
      return response(state);
    }),
  );
  render(<Environment csrf="c" onUnauthorized={vi.fn()} />);
  const user = userEvent.setup();
  await screen.findByText("DB_PASSWORD");
  await user.type(screen.getByLabelText("نام متغیر"), "bad-name");
  await user.click(screen.getByRole("button", { name: /افزودن یا ویرایش/ }));
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /ذخیره در مقصد/ })).toHaveProperty("disabled", true);
});

it("previews an env apply for one service and queues it only after confirmation", async () => {
  const bodies: Record<string, unknown>[] = [];
  const plan = {
    id: "plan-1",
    project: "shop",
    services: ["api"],
    affected: ["api"],
    envChanges: [{ scope: "", name: "APP_ENV", action: "set", exists: true }],
    preservedLimits: [{ service: "api", memoryBytes: 512 * 1048576, nanoCpus: 1_000_000_000, memSwapBytes: 0, composeMemoryBytes: 0, composeNanoCpus: 0 }],
    warnings: [],
    images: [],
    files: [],
    request: { mode: "apply" },
    target: {},
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/api/env/projects") return response([{ name: "shop", services: ["api"] }]);
      if (path === "/api/env/shop") return response(state);
      if (path === "/api/shipping/plans") {
        bodies.push(JSON.parse(init!.body as string));
        return response(plan);
      }
      if (path === "/api/shipping/jobs" && init?.method === "POST")
        return response({ id: "job-1", project: "shop", status: "queued", phase: "queued", message: "Shipment queued", updatedAt: new Date().toISOString() });
      if (path === "/api/shipping/jobs") return response([]);
      throw new Error(path);
    }),
  );
  render(<Environment csrf="c" onUnauthorized={vi.fn()} />);
  const user = userEvent.setup();
  await screen.findByText("DB_PASSWORD");
  await user.click(screen.getByRole("button", { name: /اعمال روی سرویس‌ها/ }));
  const dialog = await screen.findByRole("dialog");
  const scope = within(dialog).getByLabelText("محدودهٔ اعمال") as HTMLSelectElement;
  expect(within(dialog).queryByRole("option", { name: /shop-migrate/ })).toBeNull();
  await user.selectOptions(scope, "api");
  await user.click(within(dialog).getByRole("button", { name: "پیش‌نمایش" }));
  expect(await within(dialog).findByText(/APP_ENV/)).toBeTruthy();
  expect(within(dialog).getByText(/محدودیت‌های حفظ‌شده از سرور/)).toBeTruthy();
  expect(bodies[0]).toMatchObject({ mode: "apply", project: "shop", service: "api" });
  await user.click(within(dialog).getByRole("button", { name: "تأیید و اعمال" }));
  await waitFor(() => expect(within(dialog).getByText("Shipment queued")).toBeTruthy());
});
