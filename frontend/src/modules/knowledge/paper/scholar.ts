import { classifyIdentifier, normalizeArxiv, normalizeDoi, type ImportSource, type PaperItem } from "./model";

interface SemanticAuthor { name?: string }
interface SemanticPaper {
  paperId?: string;
  title?: string;
  year?: number;
  venue?: string;
  authors?: SemanticAuthor[];
  externalIds?: { DOI?: string; ArXiv?: string };
}

export async function searchSemanticScholar(query: string): Promise<PaperItem[]> {
  const url = `/scholar/graph/v1/paper/search?query=${encodeURIComponent(query)}&limit=8&fields=title,authors,year,venue,externalIds`;
  const response = await fetch(url);
  if (!response.ok) {
    const reason = response.status === 429 ? "请求过于频繁，稍后再试" : `状态 ${response.status}`;
    throw new Error(`Semantic Scholar 暂时无法检索（${reason}）`);
  }
  const payload = await response.json() as { data?: SemanticPaper[] };
  return (payload.data || []).filter((item) => item.title).map((item, index) => ({
    id: item.paperId || `s2-${Date.now()}-${index}`,
    title: item.title || "",
    authors: (item.authors || []).slice(0, 3).map((author) => author.name).filter(Boolean).join("、") || "佚名",
    venue: item.venue || "Semantic Scholar",
    year: item.year || new Date().getFullYear(),
    folderId: null,
    status: "unread" as const,
    important: false,
    tags: [],
    source: "semantic_scholar" as ImportSource,
    parse: "ready" as const,
    doi: item.externalIds?.DOI,
    arxiv: item.externalIds?.ArXiv,
  }));
}

interface ZoteroItem {
  key: string;
  data?: { itemType?: string; title?: string; date?: string; publicationTitle?: string; DOI?: string; creators?: Array<{ lastName?: string; firstName?: string; name?: string }> };
}

export async function searchZotero(userId: string, apiKey: string): Promise<PaperItem[]> {
  const response = await fetch(`https://api.zotero.org/users/${encodeURIComponent(userId)}/items/top?format=json&limit=20&itemType=journalArticle||conferencePaper||preprint`, {
    headers: { "Zotero-API-Key": apiKey },
  });
  if (!response.ok) throw new Error("Zotero 没有返回文献，请检查用户 ID 和 API Key");
  const items = await response.json() as ZoteroItem[];
  return items.filter((item) => item.data?.title).map((item) => {
    const creators = item.data?.creators || [];
    const authors = creators.slice(0, 3).map((creator) => creator.name || [creator.lastName, creator.firstName].filter(Boolean).join(" ")).filter(Boolean).join("、");
    const year = Number((item.data?.date || "").slice(0, 4)) || new Date().getFullYear();
    return {
      id: `zotero-${item.key}`,
      title: item.data?.title || "",
      authors: authors || "佚名",
      venue: item.data?.publicationTitle || "Zotero",
      year,
      folderId: null,
      status: "unread" as const,
      important: false,
      tags: [],
      source: "zotero" as ImportSource,
      parse: "ready" as const,
      doi: item.data?.DOI,
    };
  });
}

function draft(partial: Partial<PaperItem> & { title: string }, source: ImportSource): PaperItem {
  return {
    id: partial.id || `${source}-${Date.now()}`,
    title: partial.title,
    authors: partial.authors || "佚名",
    venue: partial.venue || "未标注",
    year: partial.year || new Date().getFullYear(),
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
  if (!response.ok) throw new Error("没有查到这个 DOI");
  const payload = await response.json() as { message?: { title?: string[]; author?: Array<{ given?: string; family?: string }>; "container-title"?: string[]; issued?: { "date-parts"?: number[][] }; DOI?: string } };
  const message = payload.message;
  const authors = (message?.author || []).slice(0, 3).map((author) => [author.family, author.given].filter(Boolean).join(" ")).filter(Boolean);
  return draft({
    id: `doi-${doi}`,
    title: message?.title?.[0] || doi,
    authors: authors.length ? `${authors.join("、")}${message?.author && message.author.length > 3 ? " 等" : ""}` : "佚名",
    venue: message?.["container-title"]?.[0] || "Crossref",
    year: message?.issued?.["date-parts"]?.[0]?.[0] || new Date().getFullYear(),
    doi: message?.DOI || doi,
  }, "doi");
}

export async function lookupArxiv(raw: string): Promise<PaperItem> {
  const arxiv = normalizeArxiv(raw);
  const response = await fetch(`/arxiv/api/query?id_list=${encodeURIComponent(arxiv)}`);
  if (!response.ok) throw new Error("没有查到这篇 arXiv 论文");
  const xml = await response.text();
  if (!xml.includes("<entry>") || xml.includes("<entry>") && xml.includes("Incorrect id format") ) {
    throw new Error("没有查到这篇 arXiv 论文");
  }
  const title = xml.match(/<title>([\s\S]*?)<\/title>/g)?.slice(1)[0]?.replace(/<\/?title>/g, "").replace(/\s+/g, " ").trim();
  if (!title) throw new Error("没有查到这篇 arXiv 论文");
  const authors = [...xml.matchAll(/<name>([\s\S]*?)<\/name>/g)].slice(0, 3).map((match) => match[1].trim());
  const published = xml.match(/<published>(\d{4})/)?.[1];
  const doi = xml.match(/<arxiv:doi[^>]*>([\s\S]*?)<\/arxiv:doi>/)?.[1]?.trim();
  return draft({
    id: `arxiv-${arxiv}`,
    title,
    authors: authors.length ? `${authors.join("、")}${xml.match(/<name>/g)!.length > 3 ? " 等" : ""}` : "佚名",
    venue: "arXiv",
    year: Number(published) || new Date().getFullYear(),
    doi,
    arxiv,
  }, "arxiv");
}

export async function lookupIdentifier(raw: string): Promise<PaperItem> {
  const kind = classifyIdentifier(raw);
  if (!kind) throw new Error("请粘贴 DOI 或 arXiv 编号");
  return kind.kind === "doi" ? lookupDoi(kind.value) : lookupArxiv(kind.value);
}
