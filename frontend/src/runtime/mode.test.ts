import { describe, expect, it } from "vitest";
import {
  getRuntimeMode,
  isDesktopRuntime,
  isLocalRuntime,
  resolveRuntimeMode,
} from "./mode";

describe("resolveRuntimeMode", () => {
  it("defaults to cloud when env is empty", () => {
    expect(resolveRuntimeMode({})).toBe("cloud");
  });

  it("recognizes valid modes case-insensitively and trims whitespace", () => {
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: " Local " })).toBe("local");
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: "DESKTOP" })).toBe("desktop");
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: "cloud" })).toBe("cloud");
  });

  it("falls back to cloud for unrecognized values", () => {
    expect(resolveRuntimeMode({ VITE_LAZYMIND_MODE: "bogus" })).toBe("cloud");
  });
});

describe("getRuntimeMode / isLocalRuntime / isDesktopRuntime", () => {
  it("reflects the actual import.meta.env at test time (cloud by default)", () => {
    expect(getRuntimeMode()).toBe("cloud");
    expect(isLocalRuntime()).toBe(false);
    expect(isDesktopRuntime()).toBe(false);
  });
});
