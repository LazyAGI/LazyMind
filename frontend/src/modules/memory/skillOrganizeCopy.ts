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
  const typedErrors: Record<string, string> = {
    skill_organize_model_unavailable: "admin.memorySkillOrganizeModelUnavailable",
    skill_organize_model_transport: "admin.memorySkillOrganizeModelUnavailable",
    skill_organize_model_response: "admin.memorySkillOrganizeModelResponse",
    skill_organize_model_timeout: "admin.memorySkillOrganizeModelTimeout",
    skill_organize_invalid_package: "admin.memorySkillOrganizeInvalidPackage",
    skill_organize_invalid_plan: "admin.memorySkillOrganizeInvalidPlan",
  };
  if (typedErrors[errorCode]) return t(typedErrors[errorCode]);
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

export function skillOrganizeErrorDetails(error: unknown) {
  const response = (error as { response?: { data?: Record<string, unknown> } } | null)?.response?.data;
  const data = response?.data;
  const detail = data && typeof data === "object" ? data as Record<string, unknown> : response;
  return {
    code: typeof detail?.code === "string" ? detail.code : "",
    message: typeof response?.message === "string" ? response.message : "",
    blockingSkills: Array.isArray(detail?.blocking_skills)
      ? [...new Set(detail.blocking_skills.filter((value): value is string => typeof value === "string" && value.trim().length > 0))]
      : [],
  };
}
