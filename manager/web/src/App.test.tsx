import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "./App";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function response(body: unknown, status = 200) {
  return { ok: status < 400, status, json: async () => body };
}
describe("manager login", () => {
  it("shows a real login error and keeps credentials out of the UI", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(response({ error: "Please sign in" }, 401))
      .mockResolvedValue(
        response({ error: "Invalid username or password" }, 401),
      );
    vi.stubGlobal("fetch", fetcher);
    render(<App />);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("نام کاربری"), "someone");
    await user.type(screen.getByLabelText("رمز عبور"), "bad-password");
    await user.click(screen.getByRole("button", { name: "ورود به پنل" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "نام کاربری یا رمز عبور درست نیست.",
    );
  });
  it("shows unavailable server state with a retry action", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    render(<App />);
    expect(
      await screen.findByRole("button", { name: "تلاش دوباره" }),
    ).toBeEnabled();
  });
});

it("submits an explicit unlimited renewal with the current revision and CSRF token", async () => {
  const project = {
    name: "shop",
    status: "suspended",
    policy: { project: "shop", suspended: true, expiresAt: null, revision: 2 },
    containers: [
      {
        id: "api",
        name: "api",
        project: "shop",
        service: "api",
        state: "exited",
        health: "",
        managed: true,
        cpu: null,
        memory: null,
        memoryLimit: null,
        networkRX: null,
        networkTX: null,
      },
    ],
    metrics: [],
  };
  const snapshot = {
    projects: [project],
    infrastructure: [],
    syncedAt: new Date().toISOString(),
    metricsAt: null,
    error: "",
    metricsError: "",
    syncIntervalSeconds: 15,
  };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/session")
      return response({ user: "admin", csrf: "test-csrf" });
    if (path === "/api/projects/shop/renew")
      return response({ status: "accepted" }, 202);
    if (path === "/api/snapshot") return response(snapshot);
    throw new Error(`unexpected request ${path} ${init?.method}`);
  });
  vi.stubGlobal("fetch", fetcher);
  render(<App />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "جزئیات shop" }));
  await user.click(screen.getByRole("button", { name: "تمدید و فعال‌سازی" }));
  await user.click(screen.getByRole("checkbox", { name: "بدون تاریخ انقضا" }));
  await user.click(screen.getByRole("button", { name: "ذخیرهٔ تغییرات" }));
  expect(await screen.findByRole("status")).toHaveTextContent(
    "درخواست ذخیره شد",
  );
  const [, init] = fetcher.mock.calls.find(
    ([path]) => path === "/api/projects/shop/renew",
  )!;
  expect(JSON.parse(init!.body as string)).toEqual({
    expiresAt: null,
    revision: 2,
  });
  expect(init!.headers).toMatchObject({ "X-CSRF-Token": "test-csrf" });
});
