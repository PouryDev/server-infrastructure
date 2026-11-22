import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ResourceModal } from "./ResourceModal";
import { type Container } from "../types";
const container: Container = {
  id: "a",
  name: "api",
  project: "shop",
  service: "shop-api",
  state: "running",
  health: "healthy",
  managed: true,
  cpu: 12,
  memory: 64000000,
  memoryLimit: 256000000,
  networkRX: null,
  networkTX: null,
};
const preview = {
  project: "shop",
  service: "shop-api",
  file: "deploy/compose.yaml",
  revision: "reviewed-source",
  branch: "main",
  gitReady: true,
  pushEnabled: false,
  containers: [{ id: "aaaaaaaaaaaa", memoryMiB: 256, cpus: 0.5 }],
  changes: ["mem_limit: 256m → 1024m", "cpus: 0.5 → 1.00"],
};
const response = (body: unknown, status = 200) => ({
  ok: status < 400,
  status,
  json: async () => body,
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function setup() {
  const applied = vi.fn();
  render(
    <ResourceModal
      container={container}
      csrf="csrf-test"
      onClose={vi.fn()}
      onApplied={applied}
      onUnauthorized={vi.fn()}
    />,
  );
  return { user: userEvent.setup(), applied };
}
it("reviews limits before applying, defaults to local and never enables push", async () => {
  const fetcher = vi.fn(async (path: string) =>
    response(
      path.endsWith("apply")
        ? { applied: true, commit: "", pushed: false, warning: "" }
        : preview,
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  const { user, applied } = setup();
  const memory = await screen.findByLabelText(/حافظهٔ RAM/);
  await waitFor(() => expect(memory).toHaveValue(256));
  expect(
    screen.getByRole("radio", { name: /فقط ذخیره و اعمال محلی/ }),
  ).toBeChecked();
  expect(
    screen.getByRole("radio", { name: /اعمال، commit و push/ }),
  ).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "1 GiB" }));
  await user.click(screen.getByRole("button", { name: "1 CPU" }));
  expect(fetcher.mock.calls.some(([p]) => p.endsWith("apply"))).toBe(false);
  await user.click(screen.getByRole("button", { name: "بازبینی تغییرات" }));
  expect(
    await screen.findByText("mem_limit: 256m → 1024m"),
  ).toBeInTheDocument();
  await user.click(
    screen.getByRole("button", { name: "تأیید و اعمال محدودیت‌ها" }),
  );
  expect(
    await screen.findByText("محدودیت‌های سرویس به‌روز شد"),
  ).toBeInTheDocument();
  const [, init] = (
    fetcher.mock.calls as unknown as [string, RequestInit][]
  ).find(([path]) => path.endsWith("apply"))!;
  expect(JSON.parse(init.body as string)).toEqual({
    project: "shop",
    service: "shop-api",
    memoryMiB: 1024,
    cpus: 1,
    gitMode: "local",
    revision: "reviewed-source",
  });
  expect(init.headers).toMatchObject({ "X-CSRF-Token": "csrf-test" });
  expect(applied).toHaveBeenCalledOnce();
});
it("blocks invalid values and requires a new review after a stale preview", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) =>
      response(
        path.endsWith("apply")
          ? { error: "resource preview is stale; preview again" }
          : preview,
        path.endsWith("apply") ? 422 : 200,
      ),
    ),
  );
  const { user } = setup();
  const memory = await screen.findByLabelText(/حافظهٔ RAM/);
  await user.clear(memory);
  await user.type(memory, "1");
  expect(
    screen.getByRole("button", { name: "بازبینی تغییرات" }),
  ).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "512 MiB" }));
  await user.click(screen.getByRole("button", { name: "بازبینی تغییرات" }));
  await user.click(
    await screen.findByRole("button", { name: "تأیید و اعمال محدودیت‌ها" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "دوباره بازبینی کنید",
  );
  expect(
    screen.queryByRole("button", { name: "تأیید و اعمال محدودیت‌ها" }),
  ).not.toBeInTheDocument();
});
it("disables automatic commits for a dirty repository", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ ...preview, gitReady: false })),
  );
  setup();
  expect(
    await screen.findByRole("radio", { name: /اعمال و commit خودکار/ }),
  ).toBeDisabled();
});
