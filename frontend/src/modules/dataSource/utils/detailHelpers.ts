import type { DocumentStatusRow } from "../constants/types";

export function isDocumentNeedSync(status: DocumentStatusRow["updateState"]) {
  return status === "new" || status === "changed" || status === "deleted";
}

export function formatNow() {
  const current = new Date();
  const pad = (value: number) => `${value}`.padStart(2, "0");
  return `${current.getFullYear()}-${pad(current.getMonth() + 1)}-${pad(
    current.getDate(),
  )} ${pad(current.getHours())}:${pad(current.getMinutes())}`;
}

export function getDirectoryLabel(path: string, sourceName: string) {
  const segments = path.split("/").filter(Boolean);
  if (segments.length <= 1) {
    return sourceName;
  }
  return segments.length > 2 ? segments[segments.length - 2] : segments[0];
}

export function getDocumentType(name: string) {
  const [, extension = "unknown"] = name.split(/\.(?=[^.]+$)/);
  return extension.toLowerCase();
}
