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
  source_roles?: Array<"cited" | "fetched" | "searched">;
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

export function getSourceFaviconUrl(source: ChatSource) {
  const hostname = isExternalSource(source) ? externalHostname(source) : "";
  return hostname
    ? `https://www.google.com/s2/favicons?domain=${encodeURIComponent(hostname)}&sz=64`
    : "";
}

function externalSourceTarget(source: ChatSource) {
  const target = source.url?.trim() || "";
  if (target && !/^(?:javascript|data|vbscript):/i.test(target)) {
    return target;
  }
  return `#source-${encodeURIComponent(getSourceCitationId(source) || "unknown")}`;
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
  return source?.source_type === "external" || Boolean(source?.url);
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

const SOURCE_ROLE_ORDER = ["cited", "fetched", "searched"] as const;

function orderedSourceRoles(roles: Iterable<string>) {
  const present = new Set(roles);
  return SOURCE_ROLE_ORDER.filter((role) => present.has(role));
}

function sourceRank(source: ChatSource) {
  const roles = source.source_roles || [];
  if (roles.includes("cited")) return 0;
  if (roles.includes("fetched")) return 1;
  if (roles.includes("searched")) return 2;
  return 0;
}

export function getDisplaySources(
  sources: ChatSourceCollection = [],
) {
  const merged = new Map<string, ChatSource>();
  const add = (
    source: ChatSource,
    fallbackRole: "cited" | "fetched" | "searched",
    index: number,
  ) => {
    const key = getSourceDedupKey(source, index);
    const current = merged.get(key);
    const roles = new Set(current?.source_roles || []);
    (source.source_roles?.length ? source.source_roles : [fallbackRole])
      .forEach((role) => roles.add(role));
    merged.set(key, {
      ...source,
      ...current,
      source_roles: orderedSourceRoles(roles),
    });
  };
  const cited = sourceValues(sources);
  cited.forEach((source, index) => add(source, "cited", index));
  return [...merged.values()];
}

export function getSearchSources(sources: ChatSourceCollection = []) {
  return [...getDisplaySources(sources)].sort((left, right) => (
    sourceRank(left) - sourceRank(right)
  ));
}

export function getSourceHref(source: ChatSource) {
  if (isExternalSource(source)) {
    return externalSourceTarget(source);
  }
  const datasetId = source.dataset_id || "default";
  const documentId = source.document_id || source.file_name || getSourceCitationId(source) || "unknown";
  const query = new URLSearchParams({
    group_name: source.group_name || "",
    segement_id: source.segement_id || "",
    number: String(source.segment_number ?? ""),
    from: "chat",
  });
  return `/lib/knowledge/knowledge/${encodeURIComponent(datasetId)}/${encodeURIComponent(documentId)}?${query}`;
}

export function openSource(source: ChatSource) {
  const href = getSourceHref(source);
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

const COMPLETE_SOURCE_MARKER = new RegExp(COMPLETE_SOURCE_MARKER_PATTERN.source, "g");
const FENCE_OPEN_PATTERN = /^(```|~~~)/;

function relocateMarkersInBlock(block: string) {
  const markers: string[] = [];
  const seen = new Set<string>();
  COMPLETE_SOURCE_MARKER.lastIndex = 0;
  const stripped = block.replace(COMPLETE_SOURCE_MARKER, (_match, displayIndex, citationId) => {
    if (!seen.has(citationId)) {
      seen.add(citationId);
      markers.push(`[${displayIndex}](#source-${citationId})`);
    }
    return "";
  });
  if (!markers.length) {
    return block;
  }
  const cleaned = stripped
    .replace(/[ \t]+([。．，,、；;：:!！?？])/g, "$1")
    .replace(/[ \t]{2,}/g, " ")
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n[ \t]+/g, "\n");
  const trailingWhitespace = cleaned.match(/\s*$/)?.[0] ?? "";
  const core = cleaned.slice(0, cleaned.length - trailingWhitespace.length);
  return `${core}${markers.join("")}${trailingWhitespace}`;
}

function relocateMarkersInProse(text: string) {
  return text.split(/(\n{2,})/).map((block, index) => {
    if (index % 2 === 1 || !block.trim()) {
      return block;
    }
    const lines = block.split("\n");
    const listLike = lines.every((line) => (
      !line.trim() || /^\s*(?:[-*+]|\d+[.)])\s+/.test(line)
    ));
    if (listLike) {
      return lines.map(relocateMarkersInBlock).join("\n");
    }
    return relocateMarkersInBlock(block);
  }).join("");
}

// Intentionally cluster citations at the paragraph (or list-item) end instead
// of after each sentence. Streaming therefore looks like:
// "Fact A. Fact B. [1][2]" rather than "Fact A [1]. Fact B [2]."
export function moveSourceMarkersToParagraphEnd(content: string) {
  const lines = content.split("\n");
  const output: string[] = [];
  let inFence = false;
  let fenceMarker = "";
  let prose: string[] = [];

  const flushProse = () => {
    if (!prose.length) {
      return;
    }
    output.push(relocateMarkersInProse(prose.join("\n")));
    prose = [];
  };

  for (const line of lines) {
    const fence = line.match(FENCE_OPEN_PATTERN);
    if (fence) {
      if (!inFence) {
        flushProse();
        inFence = true;
        fenceMarker = fence[1];
        output.push(line);
        continue;
      }
      if (line.startsWith(fenceMarker)) {
        output.push(line);
        inFence = false;
        fenceMarker = "";
        continue;
      }
    }
    if (inFence) {
      output.push(line);
    } else {
      prose.push(line);
    }
  }
  flushProse();
  return output.join("\n");
}
