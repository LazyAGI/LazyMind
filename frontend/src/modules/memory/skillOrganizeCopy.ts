const skillOrganizeStatusKeys: Record<string, string> = {
  pending: "admin.memorySkillOrganizeStatusPending",
  running: "admin.memorySkillOrganizeStatusRunning",
  organize_plan: "admin.memorySkillOrganizeStatusPlan",
  organize_draft: "admin.memorySkillOrganizeStatusDraft",
  organize_apply: "admin.memorySkillOrganizeStatusApply",
  completed: "admin.memorySkillOrganizeStatusCompleted",
  done: "admin.memorySkillOrganizeStatusCompleted",
  failed: "admin.memorySkillOrganizeStatusFailed",
  skipped: "admin.memorySkillOrganizeStatusSkipped",
  cancelled: "admin.memorySkillOrganizeStatusCancelled",
};

export function skillOrganizeStatusLabel(
  status: string,
  t: (key: string) => string,
): string {
  const key = skillOrganizeStatusKeys[status];
  return t(key || "admin.memorySkillOrganizeStatusRunning");
}

export function skillOrganizeModeLabel(
  mode: string,
  t: (key: string) => string,
): string {
  if (mode === "light") {
    return t("admin.memorySkillOrganizeLight");
  }
  if (mode === "deep") {
    return t("admin.memorySkillOrganizeDeep");
  }
  return "";
}

export function skillOrganizeErrorText(
  errorCode: string,
  rawMessage: string,
  t: (key: string) => string,
): string {
  if (errorCode === "skill_organize_draft_conflict") {
    return t("admin.memorySkillOrganizeDraftConflict");
  }
  if (
    errorCode === "skill_organize_insufficient_skills" ||
    errorCode === "skill_organize_insufficient_internal_skills" ||
    errorCode === "skill_organize_no_internal_skills"
  ) {
    return t("admin.memorySkillOrganizeMinimumWarning");
  }
  if (errorCode === "skill_maintenance_task_running") {
    return t("admin.memorySkillOrganizeTaskRunning");
  }
  const latinWords = rawMessage.match(/[A-Za-z]{4,}/g) || [];
  if (latinWords.length >= 2) {
    return t("admin.memorySkillOrganizeFailed");
  }
  if (/[\u4e00-\u9fff]/.test(rawMessage)) {
    return rawMessage;
  }
  return t("admin.memorySkillOrganizeFailed");
}
