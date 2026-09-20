import {
  listSkillAssetsPage, getSkillDraftStatus, compareSkillTreeDiff,
  compareSkillFileDiff, commitSkillDraft, discardSkillDraft,
  type SkillAssetRecord, type SkillDraftStatusRecord, type SkillDiffFileRecord,
} from "../../skillApi";

export interface PendingSkillDraft {
  skill: SkillAssetRecord;
  status?: SkillDraftStatusRecord;
  error?: string;
}
export interface SkillDraftReview extends PendingSkillDraft {
  status: SkillDraftStatusRecord;
  files: SkillDiffFileRecord[];
}
export const draftReviewError = (error: unknown): string =>
  error instanceof Error ? error.message : "admin.memorySkillDraftReviewRequestFailed";
const fail = (suffix: string): never => { throw new Error(`admin.memorySkillDraftReview${suffix}`); };
const isAgentDraft = (status: SkillDraftStatusRecord) =>
  (status.hasUncommittedDraft || status.overlayCount > 0) && Boolean(status.taskId || status.conversationId);
const signature = (status: SkillDraftStatusRecord) =>
  JSON.stringify([status.draftVersion, status.baseRevisionId, status.taskId, status.conversationId, status.hasUncommittedDraft, status.overlayCount]);

/** Only pending agent packages, without preview IO. Unknown status stays visible. */
export async function listPendingSkillDrafts(): Promise<PendingSkillDraft[]> {
  const skills = new Map<string, SkillAssetRecord>();
  for (let page = 1; ; page++) {
    const result = await listSkillAssetsPage({page, pageSize: 100});
    const before = skills.size;
    for (const skill of result.records) skills.set(skill.skillId, skill);
    if (skills.size >= result.total) break;
    if (skills.size === before) fail("IncompleteList");
  }
  const candidates = [...skills.values()].filter(skill => skill.draft.hasUncommittedDraft);
  const results: PendingSkillDraft[] = [];
  // Bound status IO for large libraries without hiding unreadable candidates.
  for (let offset = 0; offset < candidates.length; offset += 6) {
    const batch = await Promise.all(candidates.slice(offset, offset + 6).map(async skill => {
      try {
        const status = await getSkillDraftStatus(skill.skillId);
        return isAgentDraft(status) ? {skill, status} : null;
      } catch (error) { return {skill, error: draftReviewError(error)}; }
    }));
    for (const row of batch) if (row) results.push(row);
  }
  return results;
}

/** A package is the existing server transaction boundary. taskId is not a merge ID. */
export async function loadSkillDraftReview(row: PendingSkillDraft): Promise<SkillDraftReview> {
  const status = await getSkillDraftStatus(row.skill.skillId);
  if (!isAgentDraft(status)) fail("Changed");
  const tree = await compareSkillTreeDiff(row.skill.skillId);
  const changedFiles = tree.files.filter(file => file.status !== "unchanged");
  if (!changedFiles.length) fail("NoPreview");
  const files: SkillDiffFileRecord[] = [];
  for (const file of changedFiles) {
    const diff = await compareSkillFileDiff(row.skill.skillId, file.path);
    // Binary/truncated content needs the existing package editor to inspect it.
    if (diff.binary || diff.tooLarge) fail("UnsupportedPreview");
    if (diff.type !== "dir" && !diff.diffEntryLines.length && diff.status === "modified") fail("NoPreview");
    files.push(diff);
  }
  if (signature(status) !== signature(await getSkillDraftStatus(row.skill.skillId))) fail("Changed");
  return {skill: row.skill, status, files};
}

export async function applySkillDraftBatch(rows: SkillDraftReview[], isActive = () => true) {
  const succeeded: string[] = [];
  const failed: Record<string, string> = {};
  for (const row of rows) {
    if (!isActive()) break;
    try {
      const status = await getSkillDraftStatus(row.skill.skillId);
      if (signature(status) !== signature(row.status)) fail("Changed");
      if (!isActive()) break;
      // Unlike :confirm (which rereads latest version), :commit enforces reviewed version.
      const result = await commitSkillDraft(row.skill.skillId, row.status.draftVersion);
      if (!result?.revisionId) fail("RequestFailed");
      succeeded.push(row.skill.skillId);
    } catch (error) { failed[row.skill.skillId] = draftReviewError(error); }
  }
  return {succeeded, failed};
}

/** Discard one package's uncommitted draft. Signature check uses the previewed status when present. */
export async function rejectSkillDraft(row: PendingSkillDraft): Promise<void> {
  if (row.status) {
    const status = await getSkillDraftStatus(row.skill.skillId);
    if (signature(status) !== signature(row.status)) fail("Changed");
  }
  const discarded = await discardSkillDraft(row.skill.skillId);
  if (!discarded) fail("RequestFailed");
}
