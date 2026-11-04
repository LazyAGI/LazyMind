export type ShowcaseEntryType = "chat" | "work";
export type ShowcaseTechnologyType = "skill" | "workflow" | "page";

export const SHOWCASE_ENTRY_QUERY_PARAM = "showcase_entry";

export function showcaseEntryType(capabilityType: string, preferred: ShowcaseEntryType = "chat"): ShowcaseEntryType {
  if (capabilityType === "page") return preferred;
  return capabilityType === "chat" ? "chat" : "work";
}

export function showcaseTechnologyType(capabilityType: string): ShowcaseTechnologyType {
  if (capabilityType === "page") return "page";
  return capabilityType === "workflow" ? "workflow" : "skill";
}

export function showcasePagePath(pageAction: string | undefined, entryType: ShowcaseEntryType) {
  return pageAction === "open_paper_library"
    ? `/lib/knowledge/list?scene=academic_papers&${SHOWCASE_ENTRY_QUERY_PARAM}=${entryType}`
    : undefined;
}

export function parseShowcaseEntryType(value: string | null): ShowcaseEntryType | null {
  return value === "chat" || value === "work" ? value : null;
}

export function buildShowcaseLaunchPath(
  caseId: string,
  capabilityType: string,
  taskId?: string,
  entryType?: ShowcaseEntryType,
) {
  const params = buildShowcaseLaunchParams(caseId, capabilityType, taskId, entryType);
  return `/agent/chat/home?${params.toString()}`;
}

export function buildShowcaseLaunchParams(
  caseId: string,
  capabilityType: string,
  taskId?: string,
  entryType?: ShowcaseEntryType,
) {
  const params = new URLSearchParams({ showcase_case: caseId });
  if (taskId) {
    params.set("showcase_task", taskId);
  }
  params.set(SHOWCASE_ENTRY_QUERY_PARAM, showcaseEntryType(capabilityType, entryType));
  return params;
}

export function matchesShowcaseEntryType(
  capabilityType: string,
  entryType: ShowcaseEntryType,
) {
  return capabilityType === "page" || showcaseEntryType(capabilityType) === entryType;
}
