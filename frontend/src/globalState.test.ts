import { describe, expect, it } from "vitest";

describe("globalState", () => {
  it("defaults BASENAME to empty string when window.BASENAME is unset", async () => {
    const { BASENAME, defaultGlobalState } = await import("./globalState");
    expect(BASENAME).toBe("");
    expect(defaultGlobalState).toEqual({ basename: "" });
  });

  it("picks up window.BASENAME when it is set before module evaluation", async () => {
    (window as Window & { BASENAME?: string }).BASENAME = "/app";
    vi.resetModules();
    const { BASENAME, defaultGlobalState } = await import("./globalState");
    expect(BASENAME).toBe("/app");
    expect(defaultGlobalState).toEqual({ basename: "/app" });
    delete (window as Window & { BASENAME?: string }).BASENAME;
    vi.resetModules();
  });
});
