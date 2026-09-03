import { afterEach, describe, expect, it } from "vitest";
import { DEVELOPER_ACTIVE_STORAGE_KEY, setDeveloperModeActive } from "./developerMode";
import {
  SENSITIVE_WORD_FILTER_STORAGE_KEY,
  isSensitiveWordFilterEnabled,
  setSensitiveWordFilterEnabled,
  skipSensitiveFilterChatField,
} from "./sensitiveWordFilter";

describe("sensitiveWordFilter", () => {
  afterEach(() => {
    localStorage.removeItem(SENSITIVE_WORD_FILTER_STORAGE_KEY);
    localStorage.removeItem(DEVELOPER_ACTIVE_STORAGE_KEY);
  });

  it("is off by default and skips the chat filter", () => {
    expect(isSensitiveWordFilterEnabled()).toBe(false);
    expect(skipSensitiveFilterChatField()).toEqual({ skip_sensitive_filter: true });
  });

  it("still skips when the filter is on but developer mode is off", () => {
    setSensitiveWordFilterEnabled(true);
    expect(skipSensitiveFilterChatField()).toEqual({ skip_sensitive_filter: true });
  });

  it("stops skipping when developer mode and the filter are both enabled", () => {
    setDeveloperModeActive(true);
    setSensitiveWordFilterEnabled(true);
    expect(isSensitiveWordFilterEnabled()).toBe(true);
    expect(skipSensitiveFilterChatField()).toEqual({});
  });
});
