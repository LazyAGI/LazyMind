import { describe, expect, it } from "vitest";
import { isAdminRole } from "./role";

describe("isAdminRole", () => {
  it("recognizes canonical admin role strings case-insensitively", () => {
    expect(isAdminRole("admin")).toBe(true);
    expect(isAdminRole("ADMIN")).toBe(true);
    expect(isAdminRole("system-admin")).toBe(true);
    expect(isAdminRole("system_admin")).toBe(true);
  });

  it("recognizes roles ending with .admin", () => {
    expect(isAdminRole("tenant.admin")).toBe(true);
    expect(isAdminRole("org.ADMIN")).toBe(true);
  });

  it("trims whitespace before checking", () => {
    expect(isAdminRole("  admin  ")).toBe(true);
  });

  it("returns false for non-admin roles or missing input", () => {
    expect(isAdminRole("member")).toBe(false);
    expect(isAdminRole(undefined)).toBe(false);
    expect(isAdminRole("")).toBe(false);
  });
});
