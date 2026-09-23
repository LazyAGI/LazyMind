export const skillOrganizeRunningTitleKey = (status: string): string => {
  switch (status) {
    case "pending":
      return "admin.memorySkillOrganizeStagePending";
    case "organize_plan":
      return "admin.memorySkillOrganizeStagePlan";
    case "organize_draft":
      return "admin.memorySkillOrganizeStageDraft";
    case "organize_apply":
      return "admin.memorySkillOrganizeStageApply";
    default:
      return "admin.memorySkillOrganizeRunning";
  }
};

export const formatSkillOrganizeElapsed = (
  elapsedMs: number,
  t: (key: string, options?: Record<string, unknown>) => string,
): string => {
  const seconds = Math.max(0, Math.floor(elapsedMs / 1000));
  const minutes = Math.floor(seconds / 60);
  if (minutes <= 0) {
    return t("admin.memorySkillOrganizeElapsedSeconds", { count: seconds });
  }
  return t("admin.memorySkillOrganizeElapsedMinutes", {
    minutes,
    seconds: seconds % 60,
  });
};
