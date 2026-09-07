import { afterEach, beforeAll, describe, expect, it } from "vitest";
import {
  applySkipSensitiveFilterToChatPayload,
  isSensitiveWordFilterEnabled,
  setSensitiveWordFilterEnabled,
  skipSensitiveFilterChatField,
} from "./sensitiveWordFilter";

const DEVELOPER_ACTIVE_STORAGE_KEY = "lazymind:developer-active";

describe("sensitiveWordFilter", () => {
  const memory = new Map<string, string>();

  beforeAll(() => {
    const storage = {
      getItem: (key: string) => memory.get(key) ?? null,
      setItem: (key: string, value: string) => {
        memory.set(key, String(value));
      },
      removeItem: (key: string) => {
        memory.delete(key);
      },
      clear: () => {
        memory.clear();
      },
    };
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      value: storage,
    });
  });

  afterEach(() => {
    memory.clear();
  });

  it("is off by default and always sends skip_sensitive_filter", () => {
    expect(isSensitiveWordFilterEnabled()).toBe(false);
    expect(skipSensitiveFilterChatField()).toEqual({ skip_sensitive_filter: true });
  });

  it("still skips when the filter is on but developer mode is off", () => {
    setSensitiveWordFilterEnabled(true);
    expect(skipSensitiveFilterChatField()).toEqual({ skip_sensitive_filter: true });
  });

  it("does not skip when developer mode and the filter are both enabled", () => {
    localStorage.setItem(DEVELOPER_ACTIVE_STORAGE_KEY, "1");
    setSensitiveWordFilterEnabled(true);
    expect(isSensitiveWordFilterEnabled()).toBe(true);
    expect(skipSensitiveFilterChatField()).toEqual({ skip_sensitive_filter: false });
  });

  it("injects skip_sensitive_filter into chat and resume payloads", () => {
    const chat = applySkipSensitiveFilterToChatPayload(
      "/api/core/conversations:chat",
      JSON.stringify({ conversation_id: "c1" }),
    );
    expect(JSON.parse(chat)).toEqual({
      conversation_id: "c1",
      skip_sensitive_filter: true,
    });

    const resume = applySkipSensitiveFilterToChatPayload(
      "/api/core/conversations:resumeChat",
      JSON.stringify({ conversation_id: "c1" }),
    );
    expect(JSON.parse(resume).skip_sensitive_filter).toBe(true);
  });
});
