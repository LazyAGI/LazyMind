import { beforeEach, describe, expect, it, vi } from "vitest";
import { applySkillDraftBatch, listPendingSkillDrafts, loadSkillDraftReview, rejectSkillDraft } from "./skillDraftReview";
import type { SkillAssetRecord } from "../../skillApi";
const api = vi.hoisted(() => ({ listSkillAssetsPage: vi.fn(), getSkillDraftStatus: vi.fn(), compareSkillTreeDiff: vi.fn(), compareSkillFileDiff: vi.fn(), commitSkillDraft: vi.fn(), discardSkillDraft: vi.fn() }));
vi.mock("../../skillApi", () => api);
const asset = (id: string, taskId = "task"): SkillAssetRecord => ({ skillId: id, id, name: id, draft: {hasUncommittedDraft: true, taskId, version: 2} } as SkillAssetRecord);
const status = (taskId = "task") => ({taskId, conversationId: "", hasUncommittedDraft: true, draftVersion: 2, baseRevisionId: "head", overlayCount: 1});
beforeEach(() => { vi.resetAllMocks(); api.getSkillDraftStatus.mockResolvedValue(status()); });
describe("pending skill packages", () => {
  it("loads every page without catalog filters and includes conversation drafts", async () => {
    api.listSkillAssetsPage.mockResolvedValueOnce({records: [asset("a"), asset("manual", "")], total: 3}).mockResolvedValueOnce({records: [asset("conversation", "")], total: 3});
    api.getSkillDraftStatus.mockImplementation(async (id) => id === "manual" ? status("") : id === "conversation" ? {...status(""), conversationId: "c"} : status());
    expect((await listPendingSkillDrafts()).map(r => r.skill.skillId)).toEqual(["a", "conversation"]);
    expect(api.listSkillAssetsPage.mock.calls).toEqual([[{page: 1, pageSize: 100}], [{page: 2, pageSize: 100}]]);
  });
  it("surfaces unknown draft status instead of hiding it", async () => {
    api.listSkillAssetsPage.mockResolvedValue({records: [asset("a")], total: 1});
    api.getSkillDraftStatus.mockRejectedValue(new Error("offline"));
    expect((await listPendingSkillDrafts())[0].error).toContain("offline");
  });
  it("fails closed when pagination repeats", async () => {
    api.listSkillAssetsPage.mockResolvedValue({records: [asset("a")], total: 2});
    await expect(listPendingSkillDrafts()).rejects.toThrow();
  });
  it("does not make an incompletely previewed package confirmable", async () => {
    api.compareSkillTreeDiff.mockResolvedValue({files: [{path: "SKILL.md", status: "modified"}, {path: "script.py", status: "deleted"}]});
    api.compareSkillFileDiff.mockRejectedValue(new Error("preview unavailable"));
    await expect(loadSkillDraftReview({skill: asset("a"), status: status()})).rejects.toThrow("preview unavailable");
  });
  it("keeps merged package files together and commits the previewed version once", async () => {
    api.compareSkillTreeDiff.mockResolvedValue({files: [{path: "SKILL.md", status: "modified"}, {path: "old.py", status: "deleted"}]});
    api.compareSkillFileDiff.mockImplementation(async (_id, path) => ({path, status: path === "old.py" ? "deleted" : "modified", diffEntryLines: [{type: "DELETION", text: "old"}]}));
    const row = await loadSkillDraftReview({skill: asset("merged"), status: status()});
    expect(row.files.map(f => f.path)).toEqual(["SKILL.md", "old.py"]);
    api.commitSkillDraft.mockResolvedValue({revisionId: "new", revisionNo: 2});
    expect((await applySkillDraftBatch([row])).succeeded).toEqual(["merged"]);
    expect(api.commitSkillDraft).toHaveBeenCalledTimes(1);
    expect(api.commitSkillDraft).toHaveBeenCalledWith("merged", 2);
  });
  it("reports failures individually and never counts an empty response as success", async () => {
    api.commitSkillDraft.mockResolvedValueOnce({revisionId: "r"}).mockRejectedValueOnce(new Error("conflict")).mockResolvedValueOnce(null);
    const rows = ["ok", "failed", "empty"].map(id => ({skill: asset(id), status: status(), files: []}));
    const result = await applySkillDraftBatch(rows);
    expect(result.succeeded).toEqual(["ok"]);
    expect(Object.keys(result.failed)).toEqual(["failed", "empty"]);
    expect(result.failed.failed).toContain("conflict");
  });
  it("blocks a version changed since preview", async () => {
    api.getSkillDraftStatus.mockResolvedValue({...status(), draftVersion: 3});
    const result = await applySkillDraftBatch([{skill: asset("a"), status: status(), files: []}]);
    expect(result.failed.a).toContain("Changed");
    expect(api.commitSkillDraft).not.toHaveBeenCalled();
  });
  it("does not infer cross-skill merge dependencies from a shared task", async () => {
    api.commitSkillDraft.mockResolvedValue({revisionId: "r"});
    await applySkillDraftBatch([{skill: asset("only-selected"), status: status(), files: []}]);
    expect(api.commitSkillDraft).toHaveBeenCalledTimes(1);
    expect(api.commitSkillDraft).toHaveBeenCalledWith("only-selected", 2);
  });
  it("blocks binary previews and stops mutations after unmount", async () => {
    api.compareSkillTreeDiff.mockResolvedValue({files: [{path: "data.bin", status: "added"}]});
    api.compareSkillFileDiff.mockResolvedValue({path: "data.bin", binary: true});
    await expect(loadSkillDraftReview({skill: asset("a"), status: status()})).rejects.toThrow("UnsupportedPreview");
    await applySkillDraftBatch([{skill: asset("a"), status: status(), files: []}], () => false);
    expect(api.commitSkillDraft).not.toHaveBeenCalled();
  });
  it("discards one package after matching the previewed draft", async () => {
    api.discardSkillDraft.mockResolvedValue(true);
    await rejectSkillDraft({skill: asset("a"), status: status()});
    expect(api.discardSkillDraft).toHaveBeenCalledWith("a");
  });
  it("does not discard after the draft version changed", async () => {
    api.getSkillDraftStatus.mockResolvedValue({...status(), draftVersion: 9});
    await expect(rejectSkillDraft({skill: asset("a"), status: status()})).rejects.toThrow("Changed");
    expect(api.discardSkillDraft).not.toHaveBeenCalled();
  });

});
