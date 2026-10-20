export type ReadingStatus = "unread" | "reading" | "read";
export type ImportSource = "local" | "zotero" | "google_scholar" | "semantic_scholar" | "doi" | "arxiv";
export type ParseState = "ready" | "parsing" | "failed";
export type MatchMode = "all" | "any";
export type CitationStyle = "gbt" | "apa" | "mla";

export interface PaperFolder {
  id: string;
  name: string;
}

export interface PaperItem {
  id: string;
  title: string;
  authors: string;
  venue: string;
  year: number;
  folderId: string | null;
  status: ReadingStatus;
  important: boolean;
  tags: string[];
  source: ImportSource;
  parse: ParseState;
  doi?: string;
  arxiv?: string;
  incomplete?: boolean;
}

export interface PaperLibraryData {
  folders: PaperFolder[];
  papers: PaperItem[];
}

export interface FilterCondition {
  field: "tag" | "status" | "importance" | "parse" | "source";
  value: string;
  label: string;
}

export interface ImportPlan {
  fresh: PaperItem[];
  skipped: PaperItem[];
}

const doiPattern = /10\.\d{4,9}\/[-._;()/:A-Z0-9]+/i;
const arxivPattern = /(?:arxiv(?:\.org)?\/(?:abs|pdf)\/|arxiv:\s*)?((?:\d{4}\.\d{4,5}|[a-z-]+\/\d{7})(?:v\d+)?)/i;

export const sourceLabel: Record<ImportSource, string> = {
  local: "本地",
  zotero: "Zotero",
  google_scholar: "Google Scholar",
  semantic_scholar: "Semantic Scholar",
  doi: "DOI",
  arxiv: "arXiv",
};

export const statusLabel: Record<ReadingStatus, string> = {
  unread: "未读",
  reading: "阅读中",
  read: "已读",
};

export const citationLabel: Record<CitationStyle, string> = {
  gbt: "国标",
  apa: "APA",
  mla: "MLA",
};

export function emptyLibrary(): PaperLibraryData {
  return { folders: [], papers: [] };
}

export function sampleLibrary(): PaperLibraryData {
  return {
    folders: [
      { id: "ml", name: "机器学习" },
      { id: "llm", name: "大模型" },
      { id: "rag", name: "检索增强" },
      { id: "eval", name: "评测" },
    ],
    papers: [
      paper("p1", "Attention Is All You Need", "Vaswani 等", "NeurIPS", 2017, "llm", "reading", true, ["Transformer", "基线"], "zotero", "ready", "10.48550/arxiv.1706.03762", "1706.03762"),
      paper("p2", "BERT: Pre-training of Deep Bidirectional Transformers", "Devlin 等", "NAACL", 2019, "llm", "read", true, ["预训练"], "zotero"),
      paper("p3", "Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks", "Lewis 等", "NeurIPS", 2020, "rag", "reading", false, ["RAG", "检索"], "local"),
      paper("p4", "Self-RAG: Learning to Retrieve, Generate, and Critique", "Asai 等", "ICLR", 2024, "rag", "unread", true, ["RAG"], "semantic_scholar"),
      paper("p5", "Lost in the Middle: How Language Models Use Long Contexts", "Liu 等", "TACL", 2024, "llm", "unread", false, ["长上下文"], "google_scholar", "failed"),
      paper("p6", "Deep Residual Learning for Image Recognition", "He 等", "CVPR", 2016, "ml", "read", false, ["视觉"], "zotero"),
      paper("p7", "Measuring Massive Multitask Language Understanding", "Hendrycks 等", "ICLR", 2021, "eval", "unread", false, ["评测"], "google_scholar"),
    ],
  };
}

function paper(
  id: string,
  title: string,
  authors: string,
  venue: string,
  year: number,
  folderId: string,
  status: ReadingStatus,
  important: boolean,
  tags: string[],
  source: ImportSource,
  parse: ParseState = "ready",
  doi?: string,
  arxiv?: string,
): PaperItem {
  return { id, title, authors, venue, year, folderId, status, important, tags, source, parse, doi, arxiv };
}

type StoredFolder = PaperFolder & { parentId?: string | null };

export function normalizeLibrary(raw: Partial<PaperLibraryData> & { folders?: StoredFolder[] }): PaperLibraryData {
  const folders = (raw.folders || []).map((folder) => ({ id: folder.id, name: folder.name }));
  const known = new Set(folders.map((folder) => folder.id));
  const papers = (raw.papers || []).map((item) => ({
    ...item,
    folderId: item.folderId && known.has(item.folderId) ? item.folderId : null,
  }));
  return { folders, papers };
}

export function papersInView(data: PaperLibraryData, folderId: string | null) {
  if (!folderId) return data.papers;
  return data.papers.filter((item) => item.folderId === folderId);
}

export function normalizeTitle(value: string) {
  return value.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, " ").trim();
}

export function normalizeDoi(value: string) {
  const stripped = value.trim().toLowerCase()
    .replace(/^https?:\/\/(dx\.)?doi\.org\//, "")
    .replace(/^doi:/, "");
  const match = stripped.match(doiPattern);
  return (match ? match[0] : stripped).replace(/[.,;)\]]+$/g, "").replace(/(\/arxiv\.\d{4}\.\d{4,5})v\d+$/i, "$1");
}

export function normalizeArxiv(value: string) {
  const compact = value.trim().replace(/\s/g, "").replace(/\.pdf$/i, "");
  const match = compact.match(arxivPattern);
  if (!match?.[1]) return "";
  return match[1].toLowerCase().replace(/v\d+$/, "");
}

export function identifiersInText(text: string) {
  const doi = text.match(doiPattern)?.[0];
  const arxiv = text.match(/(?:arxiv:\s*)?(\d{4}\.\d{4,5})(?:v\d+)?/i)?.[1];
  const year = Number(text.match(/\b(?:19|20)\d{2}\b/)?.[0] || 0);
  return {
    doi: doi ? normalizeDoi(doi) : undefined,
    arxiv: arxiv ? normalizeArxiv(arxiv) : undefined,
    year: year || undefined,
  };
}

export function classifyIdentifier(raw: string): { kind: "doi" | "arxiv"; value: string } | null {
  const doi = normalizeDoi(raw);
  if (doiPattern.test(doi)) return { kind: "doi", value: doi };
  const arxiv = normalizeArxiv(raw);
  if (arxiv) return { kind: "arxiv", value: arxiv };
  return null;
}

export function duplicateOf(existing: PaperItem[], item: PaperItem) {
  const doi = item.doi ? normalizeDoi(item.doi) : "";
  if (doi) {
    const hit = existing.find((paper) => paper.doi && normalizeDoi(paper.doi) === doi);
    if (hit) return hit;
  }
  const arxiv = item.arxiv ? normalizeArxiv(item.arxiv) : "";
  if (arxiv) {
    const hit = existing.find((paper) => paper.arxiv && normalizeArxiv(paper.arxiv) === arxiv);
    if (hit) return hit;
  }
  const title = normalizeTitle(item.title);
  if (!title) return undefined;
  return existing.find((paper) => normalizeTitle(paper.title) === title && paper.year === item.year);
}

export function planImport(existing: PaperItem[], incoming: PaperItem[]): ImportPlan {
  const fresh: PaperItem[] = [];
  const skipped: PaperItem[] = [];
  incoming.forEach((item) => {
    const hit = duplicateOf([...existing, ...fresh], item);
    if (hit) skipped.push(hit);
    else fresh.push(item);
  });
  return { fresh, skipped };
}

function matchesCondition(item: PaperItem, condition: FilterCondition) {
  if (condition.field === "tag") return item.tags.includes(condition.value);
  if (condition.field === "status") return item.status === condition.value;
  if (condition.field === "importance") return String(item.important) === condition.value;
  if (condition.field === "parse") return item.parse === condition.value;
  return item.source === condition.value;
}

export function filterPapers(
  items: PaperItem[],
  status: ReadingStatus | "all",
  conditions: FilterCondition[],
  match: MatchMode,
  keyword = "",
) {
  const needle = keyword.trim().toLowerCase();
  return items.filter((item) => {
    if (status !== "all" && item.status !== status) return false;
    if (needle) {
      const haystack = [item.title, item.authors, item.venue, item.doi, item.arxiv, ...item.tags].join(" ").toLowerCase();
      if (!haystack.includes(needle)) return false;
    }
    if (!conditions.length) return true;
    const checks = conditions.map((condition) => matchesCondition(item, condition));
    return match === "all" ? checks.every(Boolean) : checks.some(Boolean);
  });
}

export function usablePaper(item: PaperItem) {
  return item.parse === "ready";
}

export function venueName(item: PaperItem) {
  const venue = (item.venue || "未标注").trim();
  return venue.replace(new RegExp(`[,\\s]*${item.year}$`), "").trim() || venue;
}

export function formatCitation(item: PaperItem, style: CitationStyle) {
  const authors = item.authors || "佚名";
  const title = item.title || "未命名论文";
  const venue = venueName(item);
  if (style === "gbt") return `${authors}. ${title}[J]. ${venue}, ${item.year}.`;
  if (style === "apa") return `${authors} (${item.year}). ${title}. ${venue}.`;
  return `${authors}. "${title}." ${venue}, ${item.year}.`;
}

export function paperLink(item: PaperItem) {
  if (item.doi) return `https://doi.org/${normalizeDoi(item.doi)}`;
  if (item.arxiv) return `https://arxiv.org/abs/${normalizeArxiv(item.arxiv)}`;
  return null;
}

export type YearOrder = "library" | "newest" | "oldest";

export function sortPapers(items: PaperItem[], order: YearOrder) {
  if (order === "library") return items;
  return [...items].sort((left, right) => {
    const delta = order === "newest" ? right.year - left.year : left.year - right.year;
    return delta || left.title.localeCompare(right.title);
  });
}

export function movePapers(papers: PaperItem[], ids: string[], folderId: string | null) {
  const wanted = new Set(ids);
  return papers.map((item) => wanted.has(item.id) ? { ...item, folderId } : item);
}

export function removePapers(papers: PaperItem[], ids: string[]) {
  const wanted = new Set(ids);
  return papers.filter((item) => !wanted.has(item.id));
}

export interface PaperEdits {
  title: string;
  authors: string;
  venue: string;
  year: number;
  doi?: string;
  arxiv?: string;
  tags: string[];
}

export function applyPaperEdits(item: PaperItem, edits: PaperEdits): PaperItem {
  const title = edits.title.trim();
  const doi = edits.doi?.trim() ? normalizeDoi(edits.doi) : undefined;
  const arxiv = edits.arxiv?.trim() ? normalizeArxiv(edits.arxiv) : undefined;
  return {
    ...item,
    title: title || item.title,
    authors: edits.authors.trim() || "佚名",
    venue: edits.venue.trim() || "未标注",
    year: Number.isFinite(edits.year) && edits.year > 0 ? Math.trunc(edits.year) : item.year,
    doi: doi || undefined,
    arxiv: arxiv || undefined,
    tags: edits.tags.map((tag) => tag.trim()).filter(Boolean),
    incomplete: !title,
  };
}

function bibField(block: string, name: string) {
  const match = block.match(new RegExp(`${name}\\s*=\\s*[{\"]([\\s\\S]*?)[}\"]\\s*,?`, "i"));
  return match?.[1]?.replace(/\s+/g, " ").trim() ?? "";
}

function draftPaper(partial: Partial<PaperItem> & { title: string }, source: ImportSource, index: number): PaperItem {
  return {
    id: partial.id || `import-${Date.now()}-${index}`,
    title: partial.title,
    authors: partial.authors || "佚名",
    venue: partial.venue || "未标注",
    year: partial.year || new Date().getFullYear(),
    folderId: partial.folderId ?? null,
    status: "unread",
    important: false,
    tags: partial.tags || [],
    source,
    parse: partial.parse || "ready",
    doi: partial.doi || undefined,
    arxiv: partial.arxiv || undefined,
    incomplete: partial.incomplete,
  };
}

export function parseBibTeX(text: string, source: ImportSource): PaperItem[] {
  const blocks = text.match(/@\w+\s*\{[\s\S]*?\n\}/g) ?? [];
  return blocks.map((block, index) => {
    const year = Number(bibField(block, "year")) || new Date().getFullYear();
    const arxiv = normalizeArxiv(bibField(block, "eprint") || bibField(block, "arxiv") || "");
    return draftPaper({
      title: bibField(block, "title") || "未命名论文",
      authors: bibField(block, "author").replace(/ and /g, "、") || "佚名",
      venue: bibField(block, "journal") || bibField(block, "booktitle") || "未标注",
      year,
      doi: bibField(block, "doi") || undefined,
      arxiv: arxiv || undefined,
    }, source, index);
  }).filter((item) => item.title !== "未命名论文" || item.authors !== "佚名");
}

function bibEscape(value: string) {
  return value.replace(/[{}]/g, "");
}

export function toBibTeX(items: PaperItem[]) {
  return items.map((item, index) => {
    const doi = item.doi ? `,\n  doi = {${bibEscape(item.doi)}}` : "";
    const eprint = item.arxiv ? `,\n  eprint = {${bibEscape(item.arxiv)}}` : "";
    return `@article{paper${index + 1},\n  title = {${bibEscape(item.title)}},\n  author = {${bibEscape(item.authors)}},\n  journal = {${bibEscape(item.venue)}},\n  year = {${item.year}}${doi}${eprint}\n}`;
  }).join("\n\n");
}

export function parseRIS(text: string, source: ImportSource): PaperItem[] {
  const records = text.split(/\nER\s+-\s*/i).map((record) => record.trim()).filter(Boolean);
  return records.map((record, index) => {
    const fields = new Map<string, string[]>();
    record.split(/\r?\n/).forEach((line) => {
      const match = line.match(/^([A-Z0-9]{2})\s+-\s+(.*)$/);
      if (!match) return;
      const list = fields.get(match[1]) || [];
      list.push(match[2].trim());
      fields.set(match[1], list);
    });
    const title = fields.get("TI")?.[0] || fields.get("T1")?.[0] || "";
    if (!title) return null;
    const arxiv = normalizeArxiv((fields.get("UR") || []).join(" ") || fields.get("AN")?.[0] || "");
    return draftPaper({
      title,
      authors: (fields.get("AU") || []).join("、") || "佚名",
      venue: fields.get("JO")?.[0] || fields.get("T2")?.[0] || "未标注",
      year: Number((fields.get("PY")?.[0] || "").slice(0, 4)) || new Date().getFullYear(),
      doi: fields.get("DO")?.[0],
      arxiv: arxiv || undefined,
      tags: (fields.get("KW") || []).flatMap((value) => value.split(/[;；]/)).map((tag) => tag.trim()).filter(Boolean),
    }, source, index);
  }).filter((item): item is PaperItem => Boolean(item));
}

export function toRIS(items: PaperItem[]) {
  return items.map((item) => [
    "TY  - JOUR",
    `TI  - ${item.title}`,
    ...item.authors.split("、").map((author) => `AU  - ${author}`),
    `JO  - ${item.venue}`,
    `PY  - ${item.year}`,
    item.doi ? `DO  - ${item.doi}` : "",
    item.arxiv ? `UR  - https://arxiv.org/abs/${item.arxiv}` : "",
    ...item.tags.map((tag) => `KW  - ${tag}`),
    "ER  - ",
  ].filter(Boolean).join("\n")).join("\n\n");
}

const csvHeaders = ["题名", "作者", "出处", "年份", "DOI", "arXiv", "标签", "文件夹"];

function splitCsvLine(line: string) {
  const cells: string[] = [];
  let current = "";
  let quoted = false;
  for (let index = 0; index < line.length; index += 1) {
    const char = line[index];
    if (char === '"') {
      if (quoted && line[index + 1] === '"') {
        current += '"';
        index += 1;
      } else quoted = !quoted;
    } else if (char === "," && !quoted) {
      cells.push(current.trim());
      current = "";
    } else current += char;
  }
  cells.push(current.trim());
  return cells;
}

function csvCell(value: string) {
  return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}

export function parseCSV(text: string, source: ImportSource = "local"): { papers: PaperItem[]; folderNames: Array<string | null> } {
  const lines = text.replace(/^\uFEFF/, "").split(/\r?\n/).filter((line) => line.trim());
  if (lines.length < 2) return { papers: [], folderNames: [] };
  const header = splitCsvLine(lines[0]).map((cell) => cell.toLowerCase());
  const indexOf = (...names: string[]) => header.findIndex((cell) => names.includes(cell));
  const titleIndex = indexOf("题名", "title");
  const papers: PaperItem[] = [];
  const folderNames: Array<string | null> = [];
  lines.slice(1).forEach((line, index) => {
    const cells = splitCsvLine(line);
    const title = cells[titleIndex] || "";
    if (!title) return;
    const folder = cells[indexOf("文件夹", "folder")] || "";
    papers.push(draftPaper({
      title,
      authors: cells[indexOf("作者", "authors")] || "佚名",
      venue: cells[indexOf("出处", "venue")] || "未标注",
      year: Number(cells[indexOf("年份", "year")]) || new Date().getFullYear(),
      doi: cells[indexOf("doi")] || undefined,
      arxiv: normalizeArxiv(cells[indexOf("arxiv")] || "") || undefined,
      tags: (cells[indexOf("标签", "tags")] || "").split(/[;；]/).map((tag) => tag.trim()).filter(Boolean),
    }, source, index));
    folderNames.push(folder || null);
  });
  return { papers, folderNames };
}

export function toCSV(items: PaperItem[], folders: PaperFolder[]) {
  const names = new Map(folders.map((folder) => [folder.id, folder.name]));
  const rows = items.map((item) => [
    item.title,
    item.authors,
    item.venue,
    String(item.year),
    item.doi || "",
    item.arxiv || "",
    item.tags.join(";"),
    item.folderId ? names.get(item.folderId) || "" : "",
  ].map(csvCell).join(","));
  return [csvHeaders.join(","), ...rows].join("\n");
}

export function assignFolders(data: PaperLibraryData, papers: PaperItem[], folderNames: Array<string | null>, fallbackFolderId: string | null) {
  const folders = [...data.folders];
  const next = papers.map((item, index) => {
    const name = folderNames[index]?.trim();
    if (!name) return { ...item, folderId: item.folderId ?? fallbackFolderId };
    let folder = folders.find((entry) => entry.name === name);
    if (!folder) {
      folder = { id: `folder-${name}-${folders.length}`, name };
      folders.push(folder);
    }
    return { ...item, folderId: folder.id };
  });
  return { folders, papers: next };
}
