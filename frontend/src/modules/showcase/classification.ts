export type ShowcaseEntryType = "chat" | "work";
export type ShowcaseTechnologyType = "skill" | "workflow";

export const SHOWCASE_ENTRY_QUERY_PARAM = "showcase_entry";

export function showcaseEntryType(capabilityType: string): ShowcaseEntryType {
  return capabilityType === "chat" ? "chat" : "work";
}

export function showcaseTechnologyType(capabilityType: string): ShowcaseTechnologyType {
  return capabilityType === "workflow" ? "workflow" : "skill";
}

export function parseShowcaseEntryType(value: string | null): ShowcaseEntryType | null {
  return value === "chat" || value === "work" ? value : null;
}

export function buildShowcaseLaunchPath(
  caseId: string,
  capabilityType: string,
  taskId?: string,
) {
  const params = new URLSearchParams({ showcase_case: caseId });
  if (taskId) {
    params.set("showcase_task", taskId);
  }
  params.set(SHOWCASE_ENTRY_QUERY_PARAM, showcaseEntryType(capabilityType));
  return `/agent/chat/home?${params.toString()}`;
}

export function matchesShowcaseEntryType(
  capabilityType: string,
  entryType: ShowcaseEntryType,
) {
  return showcaseEntryType(capabilityType) === entryType;
}
