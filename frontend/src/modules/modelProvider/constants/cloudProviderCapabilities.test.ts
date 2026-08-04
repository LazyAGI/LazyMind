import { describe, expect, it } from "vitest";
import {
  CLOUD_CAPABILITY_I18N_KEYS,
  CLOUD_QUICK_ACTION_PATHS,
  cloudProviderCapabilityConfigs,
} from "./cloudProviderCapabilities";

describe("cloudProviderCapabilityConfigs", () => {
  it("defines a config entry for every supported provider type", () => {
    expect(Object.keys(cloudProviderCapabilityConfigs)).toEqual([
      "local",
      "feishu",
      "notion",
      "googledrive",
    ]);
  });

  it("gives the local provider the full retrieval/task/knowledge capability set", () => {
    expect(cloudProviderCapabilityConfigs.local.enabledCapabilities).toEqual([
      "defaultRetrieval",
      "syncTask",
      "syncKnowledge",
    ]);
    expect(cloudProviderCapabilityConfigs.local.quickActions).toEqual(["knowledge", "chat"]);
  });

  it("restricts google drive to chat search only, unlike feishu/notion", () => {
    expect(cloudProviderCapabilityConfigs.googledrive.enabledCapabilities).toEqual(["chatSearch"]);
    expect(cloudProviderCapabilityConfigs.googledrive.quickActions).toEqual(["chat"]);
    expect(cloudProviderCapabilityConfigs.feishu.enabledCapabilities).toContain("linkCite");
  });

  it("gives feishu and notion distinct preview scenario keys from their connected scenario keys", () => {
    expect(cloudProviderCapabilityConfigs.feishu.previewScenarioKey).not.toBe(
      cloudProviderCapabilityConfigs.feishu.scenarioKey,
    );
    expect(cloudProviderCapabilityConfigs.local.previewScenarioKey).toBe(
      cloudProviderCapabilityConfigs.local.scenarioKey,
    );
  });
});

describe("CLOUD_CAPABILITY_I18N_KEYS", () => {
  it("provides an i18n key for every capability id used across provider configs", () => {
    const usedCapabilities = new Set(
      Object.values(cloudProviderCapabilityConfigs).flatMap((config) => [
        ...config.enabledCapabilities,
        ...config.previewCapabilities,
      ]),
    );
    usedCapabilities.forEach((capability) => {
      expect(CLOUD_CAPABILITY_I18N_KEYS[capability]).toBeTruthy();
    });
  });
});

describe("CLOUD_QUICK_ACTION_PATHS", () => {
  it("maps each quick action id to a route path", () => {
    expect(CLOUD_QUICK_ACTION_PATHS.knowledge).toBe("/lib/knowledge/list");
    expect(CLOUD_QUICK_ACTION_PATHS.chat).toBe("/agent/chat/home");
  });
});
