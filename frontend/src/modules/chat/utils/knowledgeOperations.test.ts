import { describe, expect, it } from "vitest";
import { knowledgeResourceHref, mergeKnowledgeOperations, type KnowledgeOperation } from "./knowledgeOperations";

const operation: KnowledgeOperation = {
  call_id: "call-1", action: "knowledge.create", status: "succeeded",
  items: [{ name: "论文集", resource_ref: { kind: "knowledge_base", knowledge_base_id: "ds_1" }, resource_status: "created" }],
};

describe("knowledge operation results", () => {
  it("restores and deduplicates a persisted call without losing other cards", () => {
    const previous = JSON.parse(JSON.stringify([operation]));
    expect(mergeKnowledgeOperations(previous, operation)).toEqual([operation]);
    expect(mergeKnowledgeOperations(previous, { ...operation, call_id: "call-2" })).toHaveLength(2);
  });
  it("uses local routes and never navigates to a deleted resource", () => {
    expect(knowledgeResourceHref(operation.items[0])).toBe("/lib/knowledge/detail/ds_1");
    expect(knowledgeResourceHref({ ...operation.items[0], resource_status: "deleted" })).toBeUndefined();
    expect(knowledgeResourceHref({ ...operation.items[0], resource_ref: { kind: "document", knowledge_base_id: "ds_2", document_id: "doc/1" } })).toBe("/lib/knowledge/knowledge/ds_2/doc%2F1");
  });
});
