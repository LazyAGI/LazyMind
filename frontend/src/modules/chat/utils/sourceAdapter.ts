interface BaseChatSource {
  index?: string | number;
  citation_id?: string;
  display_index?: string | number;
  title?: string;
  url?: string;
  content?: string;
  file_name?: string;
  document_id?: string;
  segement_id?: string;
  dataset_id?: string;
  group_name?: string;
  segment_number?: number;
  favicon_url?: string;
  metadata?: Record<string, unknown>;
}

export interface ExternalChatSource extends BaseChatSource {
  source_type: "external";
  title?: string;
  url?: string;
}

export interface KnowledgeBaseChatSource extends BaseChatSource {
  source_type?: "knowledge_base";
  file_name?: string;
  document_id?: string;
  dataset_id?: string;
}

export type ChatSource = ExternalChatSource | KnowledgeBaseChatSource;

function externalHostname(source: ChatSource) {
  try {
    return new URL(source.url || "").hostname;
  } catch {
    return "";
  }
}

function safeExternalUrl(source: ChatSource) {
  try {
    const url = new URL(source.url || "");
    return url.protocol === "http:" || url.protocol === "https:" ? url.toString() : "";
  } catch {
    return "";
  }
}

function normalizedExternalUrl(source: ChatSource) {
  try {
    const url = new URL(source.url || "");
    url.hash = "";
    if (url.pathname !== "/") {
      url.pathname = url.pathname.replace(/\/+$/, "");
    }
    return url.toString();
  } catch {
    return source.url || "";
  }
}

export function isExternalSource(source?: ChatSource): source is ExternalChatSource {
  return source?.source_type === "external";
}

export function getSourceCitationId(source?: ChatSource) {
  return String(source?.citation_id ?? source?.index ?? "");
}

export function getSourceLabel(source: ChatSource) {
  if (isExternalSource(source)) {
    return source.title?.trim() || externalHostname(source) || source.url || "Source";
  }
  return source.file_name?.trim() || source.title?.trim() || "Source";
}

export function getSourceSubtitle(source: ChatSource) {
  return isExternalSource(source)
    ? externalHostname(source)
    : source.group_name?.trim() || "";
}

export function getSourceIcon(source: ChatSource) {
  const metadataIcon = source.metadata?.favicon_url;
  return typeof source.favicon_url === "string"
    ? source.favicon_url
    : typeof metadataIcon === "string"
      ? metadataIcon
      : undefined;
}

export function getSourceEvidenceText(source: ChatSource) {
  return source.content || "";
}

export function getSourceDedupKey(source: ChatSource, fallbackIndex = 0) {
  if (isExternalSource(source)) {
    return `external:${normalizedExternalUrl(source) || getSourceCitationId(source) || fallbackIndex}`;
  }
  return `knowledge_base:${source.dataset_id || ""}:${source.document_id || source.file_name || getSourceCitationId(source) || fallbackIndex}`;
}

export function getSourceHref(source: ChatSource) {
  if (isExternalSource(source)) {
    return safeExternalUrl(source);
  }
  if (!source.dataset_id || source.dataset_id === "default" || !source.document_id) {
    return "";
  }
  const query = new URLSearchParams({
    group_name: source.group_name || "",
    segement_id: source.segement_id || "",
    number: String(source.segment_number ?? ""),
    from: "chat",
  });
  return `/lib/knowledge/knowledge/${encodeURIComponent(source.dataset_id)}/${encodeURIComponent(source.document_id)}?${query}`;
}

export function openSource(source: ChatSource) {
  const href = getSourceHref(source);
  if (!href) {
    return false;
  }
  window.open(href, "_blank", "noopener,noreferrer");
  return true;
}

export function findSourceByCitationId(sources: ChatSource[], citationId: string) {
  return sources.find((source) => getSourceCitationId(source) === citationId);
}

const SOURCE_LINK_PATTERN =
  String.raw`\[[^\]\n]*\]\(#(?:user-content-)?source-[^\s)]+(?:\s+"[^"\n]*")?\)`;
const COMPLETE_SOURCE_MARKER_PATTERN =
  /\[(\d+)\]\(#(?:user-content-)?source-(\d+\.\d+)(?:\s+"[^"\n]*")?\)/g;
const TRAILING_INCOMPLETE_SOURCE_MARKER_PATTERN =
  /\[(\d+)\]\(#(?:user-content-)?source-(\d+\.\d+)(?:\s+"[^"\n]*"?)?$/;
const DUPLICATE_SOURCE_MARKER_PATTERN =
  /(\[(\d+)\]\(#source-(\d+\.\d+)\))\s*[（(]\s*\[\2\]\(#source-\3\)\s*[)）]/g;
const REDUNDANT_SOURCE_URL_PATTERN = new RegExp(
  `(${SOURCE_LINK_PATTERN})\\s*[（(]\\s*(?:https?:\\/\\/|www\\.)[^\\s)）]+\\s*[)）]`,
  "g",
);

export function normalizeSourceMarkers(content: string) {
  return content
    .replace(COMPLETE_SOURCE_MARKER_PATTERN, "[$1](#source-$2)")
    .replace(DUPLICATE_SOURCE_MARKER_PATTERN, "$1")
    .replace(TRAILING_INCOMPLETE_SOURCE_MARKER_PATTERN, "[$1](#source-$2)");
}

export function stripRedundantSourceUrls(content: string) {
  return content.replace(REDUNDANT_SOURCE_URL_PATTERN, "$1");
}
