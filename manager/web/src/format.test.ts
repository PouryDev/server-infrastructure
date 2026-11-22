import { describe, expect, it } from "vitest";
import { bytes, localInput, sum, sumKnown, auditDetail } from "./format";
import type { Container } from "./types";
const container = (patch: Partial<Container>): Container => ({
  id: "a",
  name: "api",
  project: "shop",
  service: "api",
  state: "running",
  health: "",
  managed: true,
  cpu: 10,
  memory: 1024,
  memoryLimit: 2048,
  networkRX: 10,
  networkTX: 20,
  ...patch,
});
describe("resource reporting", () => {
  it("keeps paused memory visible and partial network totals explicit", () => {
    expect(sum([container({ state: "paused", memory: 2048 })], "memory")).toBe(
      2048,
    );
    expect(sumKnown([container({ networkRX: null })], "networkRX")).toBeNull();
    expect(
      sumKnown(
        [container({ networkRX: 50 }), container({ networkRX: null })],
        "networkRX",
      ),
    ).toBe(50);
  });
  it("renders before and after policy values for audit review", () => {
    expect(
      auditDetail(
        JSON.stringify({
          before: { expiresAt: null, suspended: false },
          after: { expiresAt: null, suspended: true },
        }),
      ),
    ).toContain("غیرفعال ← فعال");
    expect(auditDetail("service-name")).toBe("service-name");
  });
  it("does not turn a missing or partial sample into zero", () => {
    expect(sum([container({}), container({ cpu: null })], "cpu")).toBeNull();
    expect(bytes(null)).toBe("—");
  });
  it("does not report retained counters of stopped containers as live usage", () => {
    expect(
      sum([container({}), container({ state: "exited", cpu: 50 })], "cpu"),
    ).toBe(10);
    expect(sum([container({ state: "exited" })], "memory")).toBe(0);
  });
  it("uses local wall time for the expiration input", () => {
    const value = new Date(2026, 10, 15, 14, 30);
    expect(localInput(value)).toBe("2026-11-15T14:30");
  });
});
