import { describe, expect, it } from "vitest";
import {
  authServiceApiUrl,
  apiUrl,
  coreApiUrl,
  resolveApiBaseUrl,
  resolveApiUrl,
  resolveAuthServiceApiUrl,
  resolveCoreApiUrl,
} from "./apiBase";

describe("resolveApiBaseUrl", () => {
  it("prefers explicit env base url and trims trailing slashes", () => {
    expect(
      resolveApiBaseUrl({ VITE_API_BASE_URL: "https://api.example.com/" }, "https://origin.test"),
    ).toBe("https://api.example.com");
  });

  it("falls back to origin when env is not set", () => {
    expect(resolveApiBaseUrl({}, "https://origin.test")).toBe("https://origin.test");
  });

  it("returns empty string when neither env nor origin exist", () => {
    expect(resolveApiBaseUrl({}, "")).toBe("");
  });
});

describe("resolveApiUrl", () => {
  it("joins base url and path, ensuring a single leading slash", () => {
    expect(resolveApiUrl("foo/bar", {}, "https://origin.test")).toBe(
      "https://origin.test/foo/bar",
    );
    expect(resolveApiUrl("/foo/bar", {}, "https://origin.test")).toBe(
      "https://origin.test/foo/bar",
    );
  });

  it("returns just the normalized path when base url is empty", () => {
    expect(resolveApiUrl("foo", {}, "")).toBe("/foo");
  });
});

describe("resolveCoreApiUrl / resolveAuthServiceApiUrl", () => {
  it("prefixes core api paths and strips leading slashes from input", () => {
    expect(resolveCoreApiUrl("/conversations", {}, "https://origin.test")).toBe(
      "https://origin.test/api/core/conversations",
    );
  });

  it("prefixes auth service api paths", () => {
    expect(resolveAuthServiceApiUrl("auth/login", {}, "https://origin.test")).toBe(
      "https://origin.test/api/authservice/auth/login",
    );
  });
});

describe("apiUrl / coreApiUrl / authServiceApiUrl convenience wrappers", () => {
  it("delegate and produce a path even without a browser origin", () => {
    expect(apiUrl("/foo")).toMatch(/\/foo$/);
    expect(coreApiUrl("bar")).toMatch(/\/api\/core\/bar$/);
    expect(authServiceApiUrl("baz")).toMatch(/\/api\/authservice\/baz$/);
  });
});
