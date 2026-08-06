import { describe, expect, it } from "vitest";
import { isGoogleOAuthRedirectUriSupported } from "./redirectUri";

describe("isGoogleOAuthRedirectUriSupported", () => {
  it("allows http or https for loopback hostnames", () => {
    expect(isGoogleOAuthRedirectUriSupported("http://localhost:3000/cb")).toBe(true);
    expect(isGoogleOAuthRedirectUriSupported("https://127.0.0.1/cb")).toBe(true);
  });

  it("requires https and a public domain shape for non-loopback hosts", () => {
    expect(isGoogleOAuthRedirectUriSupported("https://example.com/cb")).toBe(true);
    expect(isGoogleOAuthRedirectUriSupported("http://example.com/cb")).toBe(false);
  });

  it("rejects raw IP hostnames and private-looking domains", () => {
    expect(isGoogleOAuthRedirectUriSupported("https://192.168.1.5/cb")).toBe(false);
    expect(isGoogleOAuthRedirectUriSupported("https://myserver.local/cb")).toBe(false);
  });

  it("returns false for invalid URLs", () => {
    expect(isGoogleOAuthRedirectUriSupported("not-a-url")).toBe(false);
  });
});
