import { describe, expect, it } from "vitest";
import { resolveRuntimeFeatures } from "./features";

describe("resolveRuntimeFeatures", () => {
  it("defaults to cloud-like feature set when no env is provided", () => {
    const features = resolveRuntimeFeatures({});
    expect(features.hideEvo).toBe(false);
    expect(features.hideRegister).toBe(false);
    expect(features.hideCloudAdmin).toBe(false);
    expect(features.localLikeAutoLogin).toBe(false);
    expect(features.allowFolderPicker).toBe(false);
    expect(features.allowOpenLogDir).toBe(false);
    expect(features.useLocalGateway).toBe(false);
  });

  it("enables local-like flags for local mode", () => {
    const features = resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: "local" });
    expect(features.hideEvo).toBe(true);
    expect(features.hideRegister).toBe(true);
    expect(features.hideCloudAdmin).toBe(true);
    expect(features.localLikeAutoLogin).toBe(true);
    expect(features.hideLocalUserControls).toBe(true);
    expect(features.hideUserGroupSurfaces).toBe(true);
    expect(features.useLocalGateway).toBe(true);
    // desktop-only flags stay off for plain "local" mode
    expect(features.allowFolderPicker).toBe(false);
    expect(features.allowOpenLogDir).toBe(false);
  });

  it("enables desktop-only flags for desktop mode", () => {
    const features = resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: "desktop" });
    expect(features.allowFolderPicker).toBe(true);
    expect(features.allowOpenLogDir).toBe(true);
    expect(features.localLikeAutoLogin).toBe(true);
  });

  it("respects explicit VITE_HIDE_EVO override even in cloud mode", () => {
    expect(resolveRuntimeFeatures({ VITE_HIDE_EVO: "true" }).hideEvo).toBe(true);
    expect(resolveRuntimeFeatures({ VITE_HIDE_EVO: "1" }).hideEvo).toBe(true);
    expect(resolveRuntimeFeatures({ VITE_HIDE_EVO: "0" }).hideEvo).toBe(false);
    expect(
      resolveRuntimeFeatures({
        VITE_LAZYMIND_MODE: "local",
        VITE_HIDE_EVO: "false",
      }).hideEvo,
    ).toBe(false);
  });

  it("ignores unparsable VITE_HIDE_EVO and falls back to mode default", () => {
    expect(
      resolveRuntimeFeatures({ VITE_LAZYMIND_MODE: "local", VITE_HIDE_EVO: "maybe" })
        .hideEvo,
    ).toBe(true);
  });
});
