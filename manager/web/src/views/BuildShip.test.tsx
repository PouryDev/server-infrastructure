import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BuildShip } from "./BuildShip";

const target = { revision: 1, mode: "ssh", host: "203.0.113.5", port: 22, user: "deploy", identity: "k", infraDir: "/srv/infrastructure", appsDir: "/srv/apps", dataDir: "/srv/data" };
const sources = [
  { id: "bodyai", subdir: "api", app: "bodyai", composeFile: "deploy/compose.yaml", platform: "linux/amd64", keepTags: 3, images: [{ service: "api", context: ".", dockerfile: "" }], buildArgNames: ["VITE_KEY"], services: ["api"] },
];
const plan = (extra = {}) => ({
  id: "plan-1",
  project: "bodyai",
  services: ["bodyai-api"],
  target,
  request: { mode: "build", service: "" },
  build: { source: "bodyai", app: "bodyai", tag: "abc1234567-dirty1700000000", head: "abc1234567", dirty: true, platform: "linux/amd64", images: [{ service: "api", context: ".", dockerfile: "" }], buildArgNames: ["VITE_KEY"], unbuilt: [] },
  images: [],
  files: [],
  warnings: [],
  envChanges: [{ scope: "", name: "API_URL", action: "set", exists: false }],
  preservedLimits: [{ service: "bodyai-api", memoryBytes: 512 * 1048576, nanoCpus: 1_000_000_000, memSwapBytes: 0, composeMemoryBytes: 0, composeNanoCpus: 0 }],
  affected: ["bodyai-api"],
  ...extra,
});
const response = (body: unknown) => ({ ok: true, status: 200, json: async () => body });
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("previews tag, dirty state, kept limits and env names; sends the compose-limits choice and edits; needs a fresh preview after an edit", async () => {
  const bodies: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/api/shipping/sources") return response(sources);
      if (path === "/api/shipping/target") return response(target);
      if (path === "/api/env/projects") return response([{ name: "bodyai", services: ["bodyai-api"] }]);
      if (path === "/api/shipping/plans") {
        bodies.push(JSON.parse(init!.body as string));
        return response(plan());
      }
      if (path === "/api/shipping/jobs" && init?.method === "POST")
        return response({ id: "job-1", project: "bodyai", status: "queued", phase: "queued", message: "Shipment queued", updatedAt: new Date().toISOString() });
      if (path === "/api/shipping/jobs") return response([]);
      throw new Error(path);
    }),
  );
  render(<BuildShip csrf="csrf-token" onUnauthorized={vi.fn()} />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /پیش‌نمایش ساخت و ارسال/ }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText("abc1234567-dirty1700000000")).toBeTruthy();
  expect(within(dialog).getByText(/تغییر ثبت‌نشده \(dirty\)/)).toBeTruthy();
  expect(within(dialog).getByText(/محدودیت‌های حفظ‌شده از سرور/)).toBeTruthy();
  expect(within(dialog).getByText(/API_URL/)).toBeTruthy();
  expect(within(dialog).getByText(/VITE_KEY/)).toBeTruthy();
  expect(bodies[0]).toMatchObject({ mode: "build", source: "bodyai", useComposeLimits: false, env: [] });
  // Edit a variable inside the review: the reviewed plan is stale until refreshed.
  await user.type(within(dialog).getByLabelText("نام متغیر"), "NEW_VAR");
  await user.type(within(dialog).getByLabelText("مقدار متغیر"), "v1");
  await user.click(within(dialog).getByRole("button", { name: /افزودن یا ویرایش/ }));
  expect(within(dialog).queryByRole("button", { name: /تأیید، ساخت و ارسال/ })).toBeNull();
  await user.click(within(dialog).getByRole("button", { name: /به‌روزرسانی پیش‌نمایش/ }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[1].env).toEqual([{ scope: "", name: "NEW_VAR", action: "set", value: "v1" }]);
  await user.click(await within(dialog).findByRole("button", { name: /تأیید، ساخت و ارسال/ }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(screen.getByText(/Job ثبت شد/)).toBeTruthy();
});

it("sends the explicit choice to use compose limits and explains the empty registry", async () => {
  const bodies: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/api/shipping/sources") return response(sources);
      if (path === "/api/shipping/target") return response(target);
      if (path === "/api/env/projects") return response([]);
      if (path === "/api/shipping/plans") {
        bodies.push(JSON.parse(init!.body as string));
        return response(plan({ preservedLimits: [] }));
      }
      if (path === "/api/shipping/jobs") return response([]);
      throw new Error(path);
    }),
  );
  render(<BuildShip csrf="c" onUnauthorized={vi.fn()} />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: /استفاده از محدودیت‌های Compose/ }));
  await user.click(screen.getByRole("button", { name: /پیش‌نمایش ساخت و ارسال/ }));
  await screen.findByRole("dialog");
  expect(bodies[0].useComposeLimits).toBe(true);
});

it("tells the owner how to register sources when none exist", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/api/shipping/sources") return response([]);
      if (path === "/api/shipping/target") return response(target);
      if (path === "/api/env/projects") return response([]);
      if (path === "/api/shipping/jobs") return response([]);
      throw new Error(path);
    }),
  );
  render(<BuildShip csrf="c" onUnauthorized={vi.fn()} />);
  expect(await screen.findByText(/builder\.local\.yaml/)).toBeTruthy();
});

it.each(["bodyai-migrate", ""])("reuses destination domain/env and migration choice %s while preserving limits", async (migrationService) => {
  const bodies: Record<string, unknown>[] = [];
  const saved = { name: "bodyai", domain: "saved.example.com", service: "bodyai-api", port: 8080, migrationService, revision: "destination-revision", envKeys: ["APP_KEY"] };
  vi.stubGlobal("fetch", vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/shipping/sources") return response(sources);
    if (path === "/api/shipping/target") return response(target);
    if (path === "/api/env/projects") return response([]);
    if (path.startsWith("/api/shipping/deployment?")) return response({ catalog: { edgePort: 8088, projects: [saved] }, services: ["bodyai-api", "bodyai-migrate", "bodyai-worker"] });
    if (path === "/api/shipping/plans") { bodies.push(JSON.parse(init!.body as string)); return response(plan({ request: { mode: "build", service: "", deployment: saved } })); }
    if (path === "/api/shipping/jobs") return response([]);
    throw new Error(path);
  }));
  render(<BuildShip csrf="csrf-token" onUnauthorized={vi.fn()} automatic/>);
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByLabelText("دامنهٔ مقصد")).toHaveValue("saved.example.com"));
  expect(screen.getByLabelText("migration مقصد")).toHaveValue(migrationService);
  expect(screen.getByText("203.0.113.5:8088")).toBeInTheDocument();
  expect(screen.queryByRole("checkbox", { name: /استفاده از محدودیت‌های Compose/ })).toBeNull();
  expect(screen.getByText(/APP_KEY/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /پیش‌نمایش ساخت و ارسال/ }));
  await screen.findByRole("dialog");
  expect(bodies[0]).toMatchObject({ mode: "build", source: "bodyai", useComposeLimits: false, includeEnv: false,
    deployment: { domain: "saved.example.com", service: "bodyai-api", migrationService, revision: "destination-revision" } });
  expect(screen.getByRole("button", { name: /تأیید، ساخت و ارسال/ })).toBeInTheDocument();
});
