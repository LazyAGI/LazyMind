import { describe, expect, it } from "vitest";
import { buildCurrentRevisionLineage } from "./versionHistoryUtils";

describe("buildCurrentRevisionLineage", () => {
  it("shows only the current head lineage and numbers it continuously", () => {
    const visible = buildCurrentRevisionLineage([
      { revisionId: "r4", parentRevisionId: "r2", isHead: true, source: "platform" },
      { revisionId: "r3", parentRevisionId: "r2", isHead: false, source: "old-branch" },
      { revisionId: "r2", parentRevisionId: "r1", isHead: false, source: "user" },
      { revisionId: "r1", parentRevisionId: "", isHead: false, source: "initial" },
    ]);

    expect(visible.map((revision) => revision.revisionId)).toEqual(["r4", "r2", "r1"]);
    expect(visible.map((revision) => revision.displayRevisionNo)).toEqual([3, 2, 1]);
  });

  it("stops safely when retained history no longer contains a parent", () => {
    const visible = buildCurrentRevisionLineage([
      { revisionId: "r5", parentRevisionId: "pruned", isHead: true },
      { revisionId: "other", parentRevisionId: "", isHead: false },
    ]);

    expect(visible).toEqual([
      {
        revisionId: "r5",
        parentRevisionId: "pruned",
        isHead: true,
        displayRevisionNo: 1,
      },
    ]);
  });
});
