import {
  render,
  screen,
  fireEvent,
  waitFor,
  cleanup,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Storefronts } from "./Storefronts";
import { request } from "../api";
vi.mock("../api", () => ({
  request: vi.fn(),
  APIError: class APIError extends Error {
    status = 0;
  },
}));
const mocked = vi.mocked(request);
const catalog = {
  images: ["storefront-api:test-sha"],
  themes: [
    {
      id: "atelier",
      name: "آتلیه",
      category: "دکور",
      layout: "editorial",
      accent: "#123456",
      background: "#ffffff",
    },
  ],
  modules: [{ id: "blog", name: "وبلاگ" }],
  jobs: [],
};
afterEach(cleanup);
beforeEach(() => mocked.mockReset());
it("walks through the launcher and submits a typed creation request with CSRF", async () => {
  mocked.mockImplementation(async (path) =>
    path === "/storefront/catalog" ? catalog : { id: "job", status: "queued" },
  );
  render(<Storefronts csrf="csrf-test" onUnauthorized={vi.fn()} />);
  const start = await screen.findByRole("button", { name: /ساخت فروشگاه/ });
  await waitFor(() => expect(start).toBeEnabled());
  fireEvent.click(start);
  fireEvent.change(screen.getByLabelText("نام فروشگاه"), {
    target: { value: "فروشگاه تست" },
  });
  fireEvent.change(screen.getByLabelText(/شناسه یکتا/), {
    target: { value: "newshop" },
  });
  fireEvent.click(screen.getByRole("button", { name: "ادامه" }));
  expect(
    await screen.findByRole("button", { name: /آتلیه/ }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "ادامه" }));
  fireEvent.change(screen.getByLabelText(/موبایل مالک فروشگاه/), {
    target: { value: "+989121234567" },
  });
  fireEvent.change(screen.getByLabelText(/موبایل توسعه‌دهنده/), {
    target: { value: "+989121234568" },
  });
  fireEvent.click(screen.getByRole("button", { name: "ادامه" }));
  fireEvent.click(
    screen.getByRole("button", { name: "ساخت و راه‌اندازی فروشگاه" }),
  );
  await waitFor(() =>
    expect(mocked).toHaveBeenCalledWith(
      "/storefront/create",
      expect.objectContaining({ method: "POST" }),
      "csrf-test",
    ),
  );
  const call = mocked.mock.calls.find(
    ([path]) => path === "/storefront/create",
  )!;
  const body = JSON.parse(call[1]!.body as string);
  expect(body).toMatchObject({
    name: "newshop",
    domain: "newshop.localhost",
    demo: true,
    image: "storefront-api:test-sha",
    ownerPhone: "+989121234567",
  });
  expect(body.key).toMatch(/^[a-f0-9-]{36}$/);
});
it("does not allow a demo store on a public domain", async () => {
  mocked.mockResolvedValue(catalog);
  render(<Storefronts csrf="csrf-test" onUnauthorized={vi.fn()} />);
  const start = await screen.findByRole("button", { name: /ساخت فروشگاه/ });
  await waitFor(() => expect(start).toBeEnabled());
  fireEvent.click(start);
  fireEvent.change(screen.getByLabelText("نام فروشگاه"), {
    target: { value: "Shop" },
  });
  fireEvent.change(screen.getByLabelText(/شناسه یکتا/), {
    target: { value: "newshop" },
  });
  fireEvent.change(screen.getByLabelText("دامنه"), {
    target: { value: "shop.example.com" },
  });
  expect(screen.getByRole("button", { name: "ادامه" })).toBeDisabled();
  expect(
    mocked.mock.calls.some(([path]) => path === "/storefront/create"),
  ).toBe(false);
});
