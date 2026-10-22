import type { ReactNode } from "react";

export type OriginKind =
  | "builtin"
  | "internal"
  | "external"
  | "skill"
  | "cloud"
  | "local"
  | "ai"
  | "custom"
  | "unknown";

export function OriginMark({
  kind,
  children,
}: {
  kind: OriginKind;
  children: ReactNode;
}) {
  return <span className={`memory-origin-mark is-${kind}`}>{children}</span>;
}

export type StatusKind =
  | "published"
  | "generating"
  | "failed"
  | "unpublished"
  | "revision"
  | "pending"
  | "merge"
  | "delete"
  | "refactor"
  | "added"
  | "removed"
  | "modified";

export function StatusMark({
  kind,
  children,
}: {
  kind: StatusKind;
  children: ReactNode;
}) {
  return <span className={`memory-status-mark is-${kind}`}>{children}</span>;
}

export function resolveSkillOriginKind(record: {
  originBuiltinSkillUid?: string;
  cloudResourceId?: string;
  category?: string;
  sourceRefType?: string;
}): OriginKind {
  if (record.originBuiltinSkillUid) {
    return "builtin";
  }
  if (record.cloudResourceId) {
    return "cloud";
  }
  if (
    record.category === "external" ||
    record.sourceRefType === "url" ||
    record.sourceRefType === "upload"
  ) {
    return "external";
  }
  if (record.category === "internal") {
    return "internal";
  }
  return "unknown";
}
