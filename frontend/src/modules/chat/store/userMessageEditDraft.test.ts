import { beforeEach, describe, expect, it } from "vitest";
import { userMessageEditDraftStore } from "./userMessageEditDraft";

describe("userMessageEditDraftStore", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("returns null for temp_ conversation ids without touching storage", () => {
    userMessageEditDraftStore.setDraft("temp_abc", { text: "hi", cites: [] });
    expect(userMessageEditDraftStore.getDraft("temp_abc")).toBeNull();
    expect(localStorage.getItem("userMsgEditDraft:temp_abc")).toBeNull();
  });

  it("returns null for an empty conversation id", () => {
    expect(userMessageEditDraftStore.getDraft("")).toBeNull();
  });

  it("round-trips a draft written via setDraft", () => {
    userMessageEditDraftStore.setDraft("conv-1", { text: "hello", cites: ["c1", "c2"] });

    expect(userMessageEditDraftStore.getDraft("conv-1")).toEqual({
      text: "hello",
      cites: ["c1", "c2"],
    });
  });

  it("returns null when no draft exists", () => {
    expect(userMessageEditDraftStore.getDraft("conv-none")).toBeNull();
  });

  it("returns null and filters invalid data when stored JSON is malformed or has wrong shape", () => {
    localStorage.setItem("userMsgEditDraft:conv-2", "not json");
    expect(userMessageEditDraftStore.getDraft("conv-2")).toBeNull();

    localStorage.setItem(
      "userMsgEditDraft:conv-3",
      JSON.stringify({ text: 123, cites: "not-array" }),
    );
    expect(userMessageEditDraftStore.getDraft("conv-3")).toBeNull();

    localStorage.setItem(
      "userMsgEditDraft:conv-4",
      JSON.stringify({ text: "ok", cites: ["a", 5, "b"] }),
    );
    expect(userMessageEditDraftStore.getDraft("conv-4")).toEqual({
      text: "ok",
      cites: ["a", "b"],
    });
  });

  it("clearDraft removes the persisted entry", () => {
    userMessageEditDraftStore.setDraft("conv-5", { text: "hi", cites: [] });
    userMessageEditDraftStore.clearDraft("conv-5");

    expect(userMessageEditDraftStore.getDraft("conv-5")).toBeNull();
  });

  it("clearDraft is a no-op for an empty conversation id", () => {
    expect(() => userMessageEditDraftStore.clearDraft("")).not.toThrow();
  });
});
