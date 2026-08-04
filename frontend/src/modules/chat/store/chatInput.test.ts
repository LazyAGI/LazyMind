import { beforeEach, describe, expect, it } from "vitest";
import { useChatInputStore } from "./chatInput";

describe("useChatInputStore", () => {
  beforeEach(() => {
    useChatInputStore.setState({ inputContents: {}, artifactRefs: {} });
  });

  it("saves and retrieves input content per conversation", () => {
    const store = useChatInputStore.getState();
    store.saveInputContent("conv-1", "hello");

    expect(useChatInputStore.getState().getInputContent("conv-1")).toBe("hello");
    expect(useChatInputStore.getState().getInputContent("conv-2")).toBe("");
  });

  it("clears input content for a single conversation without affecting others", () => {
    const store = useChatInputStore.getState();
    store.saveInputContent("conv-1", "hello");
    store.saveInputContent("conv-2", "world");

    useChatInputStore.getState().clearInputContent("conv-1");

    expect(useChatInputStore.getState().getInputContent("conv-1")).toBe("");
    expect(useChatInputStore.getState().getInputContent("conv-2")).toBe("world");
  });

  it("clearAllInputContents wipes every conversation's content", () => {
    const store = useChatInputStore.getState();
    store.saveInputContent("conv-1", "hello");
    store.saveInputContent("conv-2", "world");

    useChatInputStore.getState().clearAllInputContents();

    expect(useChatInputStore.getState().inputContents).toEqual({});
  });

  it("addArtifactRef appends and replaces refs with the same slot/sort_order", () => {
    const store = useChatInputStore.getState();
    store.addArtifactRef("conv-1", { slot: "s1", slot_id: "id1", content_type: "text" });
    store.addArtifactRef("conv-1", { slot: "s2", slot_id: "id2", content_type: "image", sort_order: 1 });

    expect(useChatInputStore.getState().getArtifactRefs("conv-1")).toHaveLength(2);

    // Replacing the ref with matching slot + sort_order (both undefined) should not duplicate.
    store.addArtifactRef("conv-1", { slot: "s1", slot_id: "id1-updated", content_type: "text" });
    const refs = useChatInputStore.getState().getArtifactRefs("conv-1");
    expect(refs).toHaveLength(2);
    expect(refs.find((r) => r.slot === "s1")?.slot_id).toBe("id1-updated");
  });

  it("removeArtifactRef removes only the matching slot/sort_order pair", () => {
    const store = useChatInputStore.getState();
    store.addArtifactRef("conv-1", { slot: "s1", slot_id: "id1", content_type: "text", sort_order: 1 });
    store.addArtifactRef("conv-1", { slot: "s1", slot_id: "id2", content_type: "text", sort_order: 2 });

    store.removeArtifactRef("conv-1", "s1", 1);

    const refs = useChatInputStore.getState().getArtifactRefs("conv-1");
    expect(refs).toHaveLength(1);
    expect(refs[0].sort_order).toBe(2);
  });

  it("clearArtifactRefs removes all refs for a conversation", () => {
    const store = useChatInputStore.getState();
    store.addArtifactRef("conv-1", { slot: "s1", slot_id: "id1", content_type: "text" });

    store.clearArtifactRefs("conv-1");

    expect(useChatInputStore.getState().getArtifactRefs("conv-1")).toEqual([]);
  });
});
