import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SkillDraftReviewPanel from "./SkillDraftReviewPanel";
const api = vi.hoisted(() => ({listSkillAssetsPage: vi.fn(), getSkillDraftStatus: vi.fn(), compareSkillTreeDiff: vi.fn(), compareSkillFileDiff: vi.fn(), commitSkillDraft: vi.fn(), discardSkillDraft: vi.fn()}));
vi.mock("../../skillApi", () => api);
const t = (key: string, options?: Record<string, unknown>) => `${key}${options ? JSON.stringify(options) : ""}`;
const skills = ["A", "B"].map(id => ({skillId: id, name: id, draft: {hasUncommittedDraft: true, version: 1, taskId: "same-task"}}));
beforeEach(() => {
  vi.resetAllMocks();
  api.listSkillAssetsPage.mockResolvedValue({records: skills, total: 2});
  api.getSkillDraftStatus.mockResolvedValue({draftVersion: 1, baseRevisionId: "head", hasUncommittedDraft: true, taskId: "same-task"});
  api.compareSkillTreeDiff.mockResolvedValue({files: [{path: "SKILL.md", status: "modified"}]});
  api.compareSkillFileDiff.mockResolvedValue({path: "SKILL.md", status: "modified", diffEntryLines: [{type: "DELETION", text: "original"}, {type: "ADDITION", text: "draft"}]});
});
describe("consolidated skill review", () => {
  it("previews all packages and preserves failed selection after partial apply", async () => {
    const applied = vi.fn();
    render(<SkillDraftReviewPanel t={t} onClose={vi.fn()} onApplied={applied} />);
    await waitFor(() => expect(screen.getByRole("checkbox", {name: "A"})).toBeEnabled());
    expect(screen.getAllByText("original")).toHaveLength(2);
    fireEvent.click(screen.getByRole("checkbox", {name: "A"}));
    fireEvent.click(screen.getByRole("checkbox", {name: "B"}));
    api.commitSkillDraft.mockResolvedValueOnce({revisionId: "r"}).mockRejectedValueOnce(new Error("B conflict"));
    api.listSkillAssetsPage.mockResolvedValue({records: [skills[1]], total: 1});
    fireEvent.click(screen.getByRole("button", {name: /admin.memorySkillDraftReviewApply/}));
    await screen.findByText("B conflict");
    await waitFor(() => expect(applied).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("checkbox", {name: "B"})).toBeChecked();
    expect(screen.queryByRole("checkbox", {name: "A"})).not.toBeInTheDocument();
  });
  it("never enables a failed preview", async () => {
    api.compareSkillFileDiff.mockRejectedValue(new Error("preview failed"));
    render(<SkillDraftReviewPanel t={t} onClose={vi.fn()} onApplied={vi.fn()} />);
    await screen.findAllByText("preview failed");
    expect(screen.getByRole("checkbox", {name: "A"})).toBeDisabled();
    expect(screen.getByRole("button", {name: /admin.memorySkillDraftReviewApply/})).toBeDisabled();
    expect(api.commitSkillDraft).not.toHaveBeenCalled();
  });
  it("guards duplicate apply clicks while the commit is in flight", async () => {
    let resolveCommit!: (value: {revisionId: string}) => void;
    api.commitSkillDraft.mockImplementation(() => new Promise(resolve => {resolveCommit = resolve;}));
    const onApplyingChange = vi.fn();
    render(<SkillDraftReviewPanel t={t} onClose={vi.fn()} onApplied={vi.fn()} onApplyingChange={onApplyingChange} />);
    await waitFor(() => expect(screen.getByRole("checkbox", {name: "A"})).toBeEnabled());
    fireEvent.click(screen.getByRole("checkbox", {name: "A"}));
    const apply = screen.getByRole("button", {name: /admin.memorySkillDraftReviewApply/});
    fireEvent.click(apply);
    fireEvent.click(apply);
    await waitFor(() => expect(api.commitSkillDraft).toHaveBeenCalledTimes(1));
    expect(onApplyingChange.mock.calls).toEqual([[true]]);
    await act(async () => resolveCommit({revisionId: "r"}));
    expect(onApplyingChange.mock.calls).toEqual([[true], [false]]);
  });
  it("ignores stale list responses after unmount", async () => {
    let resolveList!: (value: unknown) => void;
    api.listSkillAssetsPage.mockImplementation(() => new Promise(resolve => {resolveList = resolve;}));
    const onPendingCountChange = vi.fn();
    const onApplyingChange = vi.fn();
    const view = render(<SkillDraftReviewPanel t={t} onClose={vi.fn()} onApplied={vi.fn()} onPendingCountChange={onPendingCountChange} onApplyingChange={onApplyingChange} />);
    view.unmount();
    await act(async () => resolveList({records: [], total: 0}));
    expect(onPendingCountChange).not.toHaveBeenCalled();
    expect(onApplyingChange).toHaveBeenCalledWith(false);
    expect(api.compareSkillTreeDiff).not.toHaveBeenCalled();
  });
  it("opens existing details for previews that cannot be displayed", async () => {
    api.compareSkillFileDiff.mockRejectedValue(new Error("preview failed"));
    const onOpenSkill = vi.fn();
    render(<SkillDraftReviewPanel t={t} onClose={vi.fn()} onApplied={vi.fn()} onOpenSkill={onOpenSkill} />);
    const buttons = await screen.findAllByRole("button", {name: "admin.memoryCloudViewDetail"});
    fireEvent.click(buttons[0]);
    expect(onOpenSkill).toHaveBeenCalledWith("A");
  });
  it("rejects one package without committing others", async () => {
    api.discardSkillDraft.mockResolvedValue(true);
    api.listSkillAssetsPage.mockResolvedValueOnce({records: skills, total: 2}).mockResolvedValue({records: [skills[1]], total: 1});
    const applied = vi.fn();
    render(<SkillDraftReviewPanel t={t} onClose={vi.fn()} onApplied={applied} />);
    await waitFor(() => expect(screen.getAllByRole("button", {name: "admin.memorySkillDraftReviewReject"}).length).toBe(2));
    fireEvent.click(screen.getAllByRole("button", {name: "admin.memorySkillDraftReviewReject"})[0]);
    fireEvent.click(await screen.findByRole("button", {name: "admin.memorySkillDraftReviewRejectConfirmOk"}));
    await waitFor(() => expect(api.discardSkillDraft).toHaveBeenCalledWith("A"));
    expect(api.commitSkillDraft).not.toHaveBeenCalled();
    await waitFor(() => expect(applied).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("checkbox", {name: "A"})).not.toBeInTheDocument();
    expect(screen.getByRole("checkbox", {name: "B"})).toBeInTheDocument();
  });

});
