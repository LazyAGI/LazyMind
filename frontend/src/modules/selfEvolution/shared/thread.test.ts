import { describe, expect, it } from "vitest";
import {
  getThreadKnowledgeBaseId,
  getThreadModeFromPayload,
  getThreadPayloadFromRestorePayload,
  getThreadTitleFromPayload,
} from "./thread";

describe("getThreadTitleFromPayload", () => {
  it("reads a title directly from the payload", () => {
    expect(getThreadTitleFromPayload({ title: "My Thread" })).toBe("My Thread");
  });

  it("falls back to a title nested under the thread/upstream/data record", () => {
    expect(getThreadTitleFromPayload({ thread: { name: "Nested" } })).toBe("Nested");
  });

  it("returns undefined for non-record payloads", () => {
    expect(getThreadTitleFromPayload("nope" as never)).toBeUndefined();
  });
});

describe("getThreadPayloadFromRestorePayload", () => {
  it("unwraps thread.thread_payload", () => {
    const payload = { thread: { thread_payload: { inputs: { kb_id: "kb1" } } } };
    expect(getThreadPayloadFromRestorePayload(payload)).toEqual({ inputs: { kb_id: "kb1" } });
  });

  it("falls back to a top-level payload field", () => {
    expect(getThreadPayloadFromRestorePayload({ payload: { a: 1 } })).toEqual({ a: 1 });
  });

  it("returns undefined for non-record payloads", () => {
    expect(getThreadPayloadFromRestorePayload(null as never)).toBeUndefined();
  });
});

describe("getThreadKnowledgeBaseId", () => {
  it("reads a kb id from the thread payload's config record", () => {
    const payload = {
      thread: { thread_payload: { config: { kb_id: "kb-config" } } },
    };
    expect(getThreadKnowledgeBaseId(payload)).toBe("kb-config");
  });

  it("falls back to a top-level dataset_id when no thread payload is present", () => {
    expect(getThreadKnowledgeBaseId({ dataset_id: "ds1" })).toBe("ds1");
  });

  it("returns undefined for non-record payloads", () => {
    expect(getThreadKnowledgeBaseId(undefined as never)).toBeUndefined();
  });
});

describe("getThreadModeFromPayload", () => {
  it("resolves a valid mode from the thread payload", () => {
    const payload = { thread: { thread_payload: { mode: "auto" } } };
    expect(getThreadModeFromPayload(payload)).toBe("auto");
  });

  it("resolves interactive mode from top-level fields", () => {
    expect(getThreadModeFromPayload({ evolution_mode: "interactive" })).toBe("interactive");
  });

  it("returns undefined for an unrecognized mode value", () => {
    expect(getThreadModeFromPayload({ mode: "weird" })).toBeUndefined();
  });

  it("returns undefined for non-record payloads", () => {
    expect(getThreadModeFromPayload([] as never)).toBeUndefined();
  });
});
