import type { ConversationArtifact } from "@/modules/chat/store/taskCenter";

const ARTIFACT_FILE_HASH_PREFIX = "#artifact-file-";
const FILE_ID_HREF_PATTERN = /^(?:file_id:|#artifact-file-)([A-Za-z0-9_-]+)$/i;
const MARKDOWN_FILE_ID_LINK_PATTERN =
  /\[([^\]\n]+)\]\(\s*file_id:\s*([A-Za-z0-9_-]+)\s*\)/gi;
const HAS_FILE_ID_LINK_PATTERN = /\]\(\s*file_id:\s*[A-Za-z0-9_-]+\s*\)/i;

export function getFileIdFromHref(href: string): string {
  const matched = href.trim().match(FILE_ID_HREF_PATTERN);
  return matched?.[1] || "";
}

export function conversationHasFileIdLink(content: string): boolean {
  return HAS_FILE_ID_LINK_PATTERN.test(content);
}

export function normalizeArtifactFileLinks(content: string): string {
  return content.replace(
    MARKDOWN_FILE_ID_LINK_PATTERN,
    (_match, label: string, fileId: string) =>
      `[${label}](${ARTIFACT_FILE_HASH_PREFIX}${fileId})`,
  );
}

export function findArtifactByFileId(
  artifacts: ConversationArtifact[],
  fileId: string,
): ConversationArtifact | undefined {
  if (!fileId) return undefined;
  return artifacts.find(
    (artifact) =>
      artifact.artifact_id === fileId ||
      artifact.value?.file_id === fileId ||
      artifact.value?.artifact_id === fileId,
  );
}

export function getArtifactFilename(artifact: ConversationArtifact): string {
  return (
    artifact.filename ||
    artifact.value?.filename ||
    artifact.slot ||
    "artifact"
  );
}

export function getArtifactSignSource(artifact: ConversationArtifact): string {
  const value = artifact.value || {};
  if (artifact.content_type !== "file" && artifact.content_type !== "image") {
    return "";
  }
  const url = typeof value.url === "string" ? value.url.trim() : "";
  if (url) return url;
  return typeof value.path === "string" ? value.path.trim() : "";
}

export function isInlineDownloadableArtifact(
  artifact: ConversationArtifact,
): boolean {
  return artifact.content_type === "text" || artifact.content_type === "json";
}

export function getArtifactTextContent(artifact: ConversationArtifact): string {
  const value = artifact.value;
  if (!value) return "";
  if (artifact.content_type === "json") {
    try {
      return JSON.stringify(value.data ?? value, null, 2);
    } catch {
      return String(value.data ?? value ?? "");
    }
  }
  return typeof value.text === "string" ? value.text : "";
}

export function isBrowserDownloadHref(url: string): boolean {
  if (!url) return false;
  if (url.startsWith("blob:")) return true;
  if (/^https?:\/\//i.test(url)) return true;
  return url.includes("/static-files/") || url.includes("/api/core/");
}

export function appendDownloadParam(url: string): string {
  if (!url) return "";
  if (url.startsWith("blob:") || /[?&]download=1(?:&|$)/.test(url)) {
    return url;
  }
  return `${url}${url.includes("?") ? "&" : "?"}download=1`;
}
