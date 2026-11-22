import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Shipping } from "./Shipping";
const target = {
  revision: 2,
  mode: "local",
  host: "",
  port: 22,
  user: "",
  identity: "id_ed25519",
  infraDir: "/tmp/infra-manager-target/infra",
  appsDir: "/tmp/infra-manager-target/apps",
  dataDir: "/tmp/infra-manager-target/data",
};
const catalog = {
  projects: [{ name: "infra", services: ["nginx"] }],
  images: [
    { id: "sha256:123", ref: "alpine:3", size: 1024, platform: "linux/amd64" },
  ],
  identities: ["id_ed25519"],
};
const response = (body: unknown) => ({
  ok: true,
  status: 200,
  json: async () => body,
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("requires a reviewed preview before queuing an image-only shipment", async () => {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/target")) return response(target);
    if (path.endsWith("/catalog")) return response(catalog);
    if (path.endsWith("/plans"))
      return response({
        id: "plan-1",
        target,
        request: { mode: "image" },
        directory: target.infraDir,
        services: [],
        images: [
          { ...catalog.images[0], destinationRef: "manager-shipped/image:123" },
        ],
        files: [],
        warnings: [],
      });
    if (path.endsWith("/jobs") && init?.method === "POST")
      return response({
        id: "job-1",
        project: "",
        status: "queued",
        phase: "queued",
        message: "Shipment queued",
        updatedAt: new Date().toISOString(),
      });
    if (path.endsWith("/jobs")) return response([]);
    throw new Error(path);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<Shipping csrf="csrf-token" onUnauthorized={vi.fn()} />);
  const user = userEvent.setup();
  await user.click(
    await screen.findByRole("button", { name: /فقط ارسال Image/ }),
  );
  await user.selectOptions(
    screen.getByRole("combobox", { name: "image محلی" }),
    "alpine:3",
  );
  await user.click(screen.getByRole("button", { name: "پیش‌نمایش ارسال" }));
  expect(
    await screen.findByRole("dialog", { name: "بررسی و تأیید ارسال" }),
  ).toBeInTheDocument();
  expect(
    fetcher.mock.calls.some(
      ([p, i]) => p.endsWith("/jobs") && i?.method === "POST",
    ),
  ).toBe(false);
  await user.click(
    screen.getByRole("button", { name: "تأیید و شروع ارسال image" }),
  );
  expect(
    await screen.findByText(
      "Job ثبت شد؛ می‌توانید صفحه را ببندید و بعداً نتیجه را ببینید.",
    ),
  ).toBeInTheDocument();
  const [, init] = fetcher.mock.calls.find(
    ([p, i]) => p.endsWith("/jobs") && i?.method === "POST",
  )!;
  expect(JSON.parse(init!.body as string)).toEqual({ planId: "plan-1" });
  expect(init!.headers).toMatchObject({ "X-CSRF-Token": "csrf-token" });
});
it("saves a global SSH profile without automatically probing or deploying", async () => {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/target"))
      return response(
        init?.method === "PUT"
          ? { ...target, ...JSON.parse(init.body as string), revision: 3 }
          : target,
      );
    if (path.endsWith("/catalog")) return response(catalog);
    if (path.endsWith("/jobs")) return response([]);
    throw new Error(path);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<Shipping csrf="token" onUnauthorized={vi.fn()} />);
  const user = userEvent.setup();
  await user.click(
    await screen.findByRole("button", { name: "تنظیم مقصد سراسری" }),
  );
  await user.click(screen.getByRole("button", { name: "سرور از طریق SSH" }));
  await user.type(screen.getByLabelText("IP یا Host مقصد"), "127.0.0.1");
  await user.type(screen.getByLabelText("کاربر SSH"), "deploy");
  await user.click(screen.getByRole("button", { name: "ذخیرهٔ مقصد سراسری" }));
  expect(await screen.findByText(/مقصد سراسری ذخیره شد/)).toBeInTheDocument();
  const [, init] = fetcher.mock.calls.find(
    ([p, i]) => p.endsWith("/target") && i?.method === "PUT",
  )!;
  expect(JSON.parse(init!.body as string)).toMatchObject({
    revision: 2,
    mode: "ssh",
    host: "127.0.0.1",
    user: "deploy",
    infraDir: "/srv/infrastructure",
  });
  expect(
    fetcher.mock.calls.some(
      ([p]) => p.endsWith("/probe") || p.endsWith("/plans"),
    ),
  ).toBe(false);
});
it("keeps server limits by default and sends an explicit opt-in to use compose limits", async () => {
  const bodies: Record<string, unknown>[] = [];
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith("/target")) return response(target);
    if (path.endsWith("/catalog"))
      return response({ ...catalog, projects: [{ name: "shop", services: ["api"] }] });
    if (path.endsWith("/plans")) {
      bodies.push(JSON.parse(init!.body as string));
      return response({
        id: "plan-2",
        target,
        request: { mode: "deploy", project: "shop" },
        directory: "/x",
        services: ["api"],
        images: [],
        files: [],
        warnings: [],
        preservedLimits: [
          { service: "api", memoryBytes: 536870912, nanoCpus: 1000000000, memSwapBytes: 0, composeMemoryBytes: 0, composeNanoCpus: 0 },
        ],
      });
    }
    if (path.endsWith("/jobs")) return response([]);
    throw new Error(path);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<Shipping csrf="t" onUnauthorized={vi.fn()} />);
  const user = userEvent.setup();
  await user.selectOptions(await screen.findByLabelText("پروژه برای ارسال"), "shop");
  await user.click(screen.getByRole("button", { name: "پیش‌نمایش ارسال" }));
  expect(await screen.findByText(/محدودیت‌های حفظ‌شده از سرور/)).toBeInTheDocument();
  expect(bodies[0].useComposeLimits).toBe(false);
  await user.click(screen.getByRole("button", { name: "بازگشت" }));
  await user.click(screen.getByRole("checkbox", { name: /استفاده از محدودیت‌های Compose/ }));
  await user.click(screen.getByRole("button", { name: "پیش‌نمایش ارسال" }));
  await screen.findByRole("dialog");
  expect(bodies[1].useComposeLimits).toBe(true);
});
