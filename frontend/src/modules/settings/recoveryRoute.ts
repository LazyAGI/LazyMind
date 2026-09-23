import type { RecoveryKind } from "./recoveryApi";

export type RecoveryView = "archive" | "trash";

export const RECOVERY_ARCHIVE_PATH = "/settings?section=recovery&view=archive";

export function getRecoveryArchivePath(kind: RecoveryKind) {
  return `${RECOVERY_ARCHIVE_PATH}&kind=${kind}`;
}

export function recoveryViewFromSearchParams(searchParams: URLSearchParams): RecoveryView {
  return searchParams.get("view") === "archive" ? "archive" : "trash";
}

export function searchParamsForRecoveryView(
  searchParams: URLSearchParams,
  view: RecoveryView,
) {
  const nextSearchParams = new URLSearchParams(searchParams);
  if (view === "archive") {
    nextSearchParams.set("view", "archive");
  } else {
    nextSearchParams.delete("view");
  }
  return nextSearchParams;
}
