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
  image_url?: string;
  image_urls?: Array<string | { url?: string }>;
  image_markdown?: string;
  source_roles?: Array<"cited" | "searched">;
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
export type ChatSourceCollection = ChatSource[] | Record<string, ChatSource> | null;

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

function sourceValues(collection: ChatSourceCollection) {
  return (Array.isArray(collection)
    ? collection
    : Object.entries(collection || {}).map(([index, source]) => (
      source?.index || source?.citation_id ? source : { ...source, index }
    ))).filter(Boolean);
}

export function getCitationSources(sources: ChatSourceCollection = []) {
  return sourceValues(sources);
}

export function getDisplaySources(
  sources: ChatSourceCollection = [],
) {
  const merged = new Map<string, ChatSource>();
  const add = (source: ChatSource, fallbackRole: "cited" | "searched", index: number) => {
    const key = getSourceDedupKey(source, index);
    const current = merged.get(key);
    const roles = new Set(current?.source_roles || []);
    (source.source_roles?.length ? source.source_roles : [fallbackRole])
      .forEach((role) => roles.add(role));
    merged.set(key, { ...source, ...current, source_roles: [...roles] });
  };
  const cited = sourceValues(sources);
  cited.forEach((source, index) => add(source, "cited", index));
  return [...merged.values()];
}

export function getSearchSources(sources: ChatSourceCollection = []) {
  const displaySources = getDisplaySources(sources);
  if (!sourceValues(sources).some((source) => source.source_roles?.length)) return displaySources;
  const searchedSources = displaySources.filter((source) => source.source_roles?.includes("searched"));
  return searchedSources;
}

function comparableImageUrl(value: string) {
  try {
    const url = new URL(value, "http://lazymind.local");
    if (url.pathname.includes("/static-files/")) url.search = "";
    return `${url.host}${url.pathname}${url.search}`;
  } catch {
    return value.trim();
  }
}

function getSourceImageUrls(source: ChatSource) {
  const metadata = source.metadata || {};
  const values: unknown[] = [source.image_url, metadata.image_url];
  values.push(...(source.image_urls || []));
  if (Array.isArray(metadata.image_urls)) values.push(...metadata.image_urls);
  const markdown = source.image_markdown || "";
  values.push(...[...markdown.matchAll(/!\[[^\]]*\]\(([^)]+)\)/g)].map((match) => match[1]));
  return values
    .map((value) => typeof value === "string" ? value : (value as { url?: string })?.url || "")
    .filter(Boolean)
    .map(comparableImageUrl);
}

export function findSourceByImageUrl(sources: ChatSource[], imageUrl: string) {
  const target = comparableImageUrl(imageUrl);
  return sources.find((source) => getSourceImageUrls(source).includes(target));
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
