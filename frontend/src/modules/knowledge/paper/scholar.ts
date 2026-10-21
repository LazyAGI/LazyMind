import { classifyIdentifier, normalizeDoi, type ImportSource, type PaperItem } from "./model";
import { isArxivInput, requestArxivMetadata } from "./arxivImport";

function citationCount(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

function draft(partial: Partial<PaperItem> & { title: string }, source: ImportSource): PaperItem {
  return {
    id: partial.id || `${source}-${Date.now()}`,
    title: partial.title,
    authors: partial.authors || "佚名",
    venue: partial.venue || "未标注",
    year: partial.year || new Date().getFullYear(),
    citationCount: citationCount(partial.citationCount),
    folderId: null,
    status: "unread",
    important: false,
    tags: [],
    source,
    parse: "ready",
    doi: partial.doi,
    arxiv: partial.arxiv,
  };
}

export async function lookupDoi(raw: string): Promise<PaperItem> {
  const doi = normalizeDoi(raw);
  const response = await fetch(`https://api.crossref.org/works/${encodeURIComponent(doi)}`);
  if (!response.ok) throw new Error(`DOI 查询失败（HTTP ${response.status}）${response.status === 404 ? "：没有查到这个 DOI" : ""}`);
  const payload = await response.json() as { message?: { title?: string[]; author?: Array<{ given?: string; family?: string }>; "container-title"?: string[]; issued?: { "date-parts"?: number[][] }; DOI?: string; "is-referenced-by-count"?: number } };
  const message = payload.message;
  if (!Array.isArray(message?.title) || typeof message.title[0] !== "string" || !message.title[0].trim()) {
    throw new Error("Crossref 未返回有效的文献标题，导入已停止");
  }
  const authors = (message?.author || []).slice(0, 3).map((author) => [author.family, author.given].filter(Boolean).join(" ")).filter(Boolean);
  return draft({
    id: `doi-${doi}`,
    title: message.title[0].trim(),
    authors: authors.length ? `${authors.join("、")}${message?.author && message.author.length > 3 ? " 等" : ""}` : "佚名",
    venue: message?.["container-title"]?.[0] || "Crossref",
    year: message?.issued?.["date-parts"]?.[0]?.[0] || new Date().getFullYear(),
    citationCount: citationCount(message["is-referenced-by-count"]),
    doi: message?.DOI || doi,
  }, "doi");
}

export async function lookupArxiv(raw: string): Promise<PaperItem> {
  const paper = await requestArxivMetadata(raw);
  return {
    ...draft({
      id: `arxiv-${paper.base_id}`, title: paper.title, authors: paper.authors.join("、"),
      venue: paper.venue, year: paper.year, doi: paper.doi || undefined, arxiv: paper.base_id,
    }, "arxiv"),
    arxivVersion: paper.version,
    abstract: paper.abstract,
    sourceUrl: paper.pdf_url,
  };
}

export async function lookupIdentifier(raw: string): Promise<PaperItem> {
  if (isArxivInput(raw)) return lookupArxiv(raw);
  const kind = classifyIdentifier(raw);
  if (!kind) throw new Error("请粘贴 DOI 或 arXiv 编号");
  return kind.kind === "doi" ? lookupDoi(kind.value) : lookupArxiv(raw);
}
