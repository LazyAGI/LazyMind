import { v4 as uuidv4 } from 'uuid';
export type ReadingStatus = "unread" | "reading" | "read";
export type LibraryView = "all" | "important" | "incomplete" | "directory";
export type ImportSource = "local" | "zotero" | "google_scholar" | "semantic_scholar" | "doi" | "arxiv" | "web" | "unknown";
export type PaperImportMethod = "upload" | "link" | "bibliography" | "expansion" | "references";
export type ParseState = "ready" | "parsing" | "failed";
export type CitationStyle = "gbt" | "apa" | "mla";
export const paperSourceVersionLabels: Record<string, string> = { submittedVersion: '投稿稿', acceptedVersion: '录用稿', publishedVersion: '出版稿' };
export interface PaperFolder {
    revision?: number;
    id: string;
    name: string;
    parentId?: string | null;
}
export interface PaperMetadata {
    title: string;
    authors: string[];
    venue: string;
    year: number;
    abstract?: string;
    identifiers: {
        doi?: string;
        arxiv?: string;
    };
    citationCount?: number;
}
export interface PaperRecord {
    id: string;
    revision: number;
    academicWorkId?: string;
    metadata: PaperMetadata;
    folderId: string | null;
    status: ReadingStatus;
    important: boolean;
    tags: string[];
    source: ImportSource;
    importMethod?: PaperImportMethod;
    parse: ParseState;
    arxivVersion?: string;
    sourceUrl?: string;
    processingError?: string;
    incomplete?: boolean;
    associationConflict?: string;
    attachmentIds: string[];
    noteIds: string[];
}
export function parseAuthorInput(value: string): string[] {
    return value.split(/[、;；\n]+/).map(name => name.trim()).filter(Boolean);
}
export function createPaperRecord(metadata: Partial<PaperMetadata> & {
    title: string;
}, fields: Partial<Omit<PaperRecord, 'metadata'>> = {}): PaperRecord {
    return { id: uuidv4(), revision: 0, folderId: null, status: 'unread', important: false, tags: [], source: 'unknown', parse: 'ready', attachmentIds: [], noteIds: [], ...fields,
        metadata: { authors: [], venue: '', year: 0, ...metadata, identifiers: { ...metadata.identifiers } } };
}
export function descendantFolderIds(folders: PaperFolder[], parentId: string): Set<string> {
    const ids = new Set([parentId]);
    const children = new Map<string, string[]>();
    for (const folder of folders)
        if (folder.parentId)
            children.set(folder.parentId, [...(children.get(folder.parentId) || []), folder.id]);
    const pending = [parentId];
    while (pending.length)
        for (const child of children.get(pending.pop()!) || [])
            if (!ids.has(child)) {
                ids.add(child);
                pending.push(child);
            }
    return ids;
}
export function paperFolderPath(folders: PaperFolder[], folderId?: string | null): string {
    const parts: string[] = [];
    const seen = new Set<string>();
    const byId = new Map(folders.map(folder => [folder.id, folder]));
    while (folderId && !seen.has(folderId)) {
        seen.add(folderId);
        const folder = byId.get(folderId);
        if (!folder)
            break;
        parts.unshift(folder.name);
        folderId = folder.parentId;
    }
    return parts.join(' / ');
}
export interface PaperLibraryData {
    folders: PaperFolder[];
    papers: PaperRecord[];
    attachments: import('./libraryModel').Attachment[];
    notes: import('./libraryModel').Note[];
}
export interface ImportPlan {
    fresh: PaperRecord[];
    skipped: PaperRecord[];
}
const doiPattern = /10\.\d{4,9}\/[-._;()/:A-Z0-9]+/i;
const arxivPattern = /(?:arxiv(?:\.org)?\/(?:abs|pdf)\/|arxiv[.:]\s*)?((?:\d{4}\.\d{4,5}|[a-z-]+\/\d{7})(?:v\d+)?)/i;
export const sourceLabel: Record<ImportSource, string> = {
    local: "本地",
    zotero: "Zotero",
    google_scholar: "Google Scholar",
    semantic_scholar: "Semantic Scholar",
    doi: "DOI",
    arxiv: "arXiv",
    web: "网站",
    unknown: "未记录",
};
export const importMethodLabel: Record<PaperImportMethod, string> = {
    upload: "上传 PDF", link: "链接导入", bibliography: "题录导入", expansion: "自动扩展", references: "参考文献导入",
};
export function safePaperUrl(value?: string): string | undefined {
    if (!value)
        return undefined;
    try {
        const url = new URL(value);
        return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : undefined;
    }
    catch {
        return undefined;
    }
}
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
export function papersInView(data: PaperLibraryData, folderId: string | null) {
    if (!folderId)
        return data.papers;
    return data.papers.filter((item) => item.folderId === folderId);
}
export function filterLibraryView(items: PaperRecord[], view: LibraryView) {
    if (view === "important")
        return items.filter((item) => item.important);
    if (view === "incomplete")
        return items.filter((item) => item.incomplete || item.parse === "failed");
    return items;
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
    if (!match?.[1])
        return "";
    return match[1].toLowerCase().replace(/v\d+$/, "");
}
export function identifiersInText(text: string) {
    const doi = text.match(doiPattern)?.[0];
    const arxiv = text.match(/(?:arxiv(?:\.org)?\/(?:abs|pdf)\/|arxiv[.:]\s*)((?:\d{4}\.\d{4,5}|[a-z-]+\/\d{7})(v\d+)?)/i);
    const year = Number(text.match(/\b(?:19|20)\d{2}\b/)?.[0] || 0);
    return {
        doi: doi ? normalizeDoi(doi) : undefined,
        arxiv: arxiv?.[1] ? normalizeArxiv(arxiv[1]) : undefined,
        arxivVersion: arxiv?.[2]?.toLowerCase(),
        year: year || undefined,
    };
}
export function classifyIdentifier(raw: string): {
    kind: "doi" | "arxiv";
    value: string;
} | null {
    const doi = normalizeDoi(raw);
    if (doiPattern.test(doi))
        return { kind: "doi", value: doi };
    const arxiv = normalizeArxiv(raw);
    if (arxiv)
        return { kind: "arxiv", value: arxiv };
    return null;
}
export function duplicateOf(existing: PaperRecord[], item: PaperRecord) {
    const doi = normalizeDoi(item.metadata.identifiers.doi || ''), arxiv = normalizeArxiv(item.metadata.identifiers.arxiv || '');
    return existing.find(paper => {
        const otherDoi = normalizeDoi(paper.metadata.identifiers.doi || ''), otherArxiv = normalizeArxiv(paper.metadata.identifiers.arxiv || '');
        if (arxiv && otherArxiv)
            return arxiv === otherArxiv;
        if (doi && otherDoi)
            return doi === otherDoi;
        return !!item.academicWorkId && item.academicWorkId === paper.academicWorkId;
    });
}
export function planImport(existing: PaperRecord[], incoming: PaperRecord[]): ImportPlan {
    const fresh: PaperRecord[] = [];
    const skipped: PaperRecord[] = [];
    incoming.forEach((item) => {
        const hit = duplicateOf([...existing, ...fresh], item);
        if (hit)
            skipped.push(hit);
        else
            fresh.push(item);
    });
    return { fresh, skipped };
}
export function filterPapers(items: PaperRecord[], keyword = '') {
    const needle = keyword.trim().toLowerCase();
    return needle ? items.filter(paper => [paper.metadata.title, ...paper.metadata.authors, paper.metadata.venue, paper.metadata.identifiers.doi, paper.metadata.identifiers.arxiv, ...paper.tags].join(' ').toLowerCase().includes(needle)) : items;
}
export function venueName(item: PaperRecord) {
    const venue = (item.metadata.venue || "未标注").trim();
    return venue.replace(new RegExp(`[,\\s]*${item.metadata.year}$`), "").trim() || venue;
}
export function formatCitation(item: PaperRecord, style: CitationStyle) {
    const authors = item.metadata.authors.join("、") || "佚名";
    const title = item.metadata.title || "未命名论文";
    const venue = venueName(item);
    if (style === "gbt")
        return `${authors}. ${title}[J]. ${venue}, ${item.metadata.year}.`;
    if (style === "apa")
        return `${authors} (${item.metadata.year}). ${title}. ${venue}.`;
    return `${authors}. "${title}." ${venue}, ${item.metadata.year}.`;
}
export function paperLink(item: PaperRecord) {
    if (item.metadata.identifiers.doi)
        return `https://doi.org/${normalizeDoi(item.metadata.identifiers.doi)}`;
    if (item.metadata.identifiers.arxiv)
        return `https://arxiv.org/abs/${normalizeArxiv(item.metadata.identifiers.arxiv)}`;
    return null;
}
export function movePapers(papers: PaperRecord[], ids: string[], folderId: string | null) {
    const wanted = new Set(ids);
    return papers.map((item) => wanted.has(item.id) ? { ...item, folderId } : item);
}
export interface PaperEdits {
    abstract?: string;
    title: string;
    authors: string;
    venue: string;
    year: number;
    doi?: string;
    arxiv?: string;
    tags: string[];
}
export function applyPaperEdits(item: PaperRecord, edits: PaperEdits): PaperRecord {
    const title = edits.title.trim();
    const doi = edits.doi?.trim() ? normalizeDoi(edits.doi) : undefined;
    const arxiv = edits.arxiv?.trim() ? normalizeArxiv(edits.arxiv) : undefined;
    return {
        ...item,
        tags: edits.tags.map((tag) => tag.trim()).filter(Boolean),
        incomplete: !title,
        metadata: {
            ...item.metadata,
            title: title || item.metadata.title,
            abstract: edits.abstract === undefined ? item.metadata.abstract : edits.abstract.trim() || undefined,
            authors: parseAuthorInput(edits.authors.trim() || "佚名"),
            venue: edits.venue.trim() || "未标注",
            year: Number.isFinite(edits.year) && edits.year > 0 ? Math.trunc(edits.year) : item.metadata.year,
            identifiers: { doi: doi || undefined, arxiv: arxiv || undefined }
        }
    };
}
function bibRecords(text: string): Map<string, string>[] {
    const records: Map<string, string>[] = [];
    let cursor = 0;
    const fail = () => new Error(`BibTeX 第 ${records.length + 1} 条格式错误，请检查括号、引号和字段`);
    const whitespace = () => {
        while (cursor < text.length) {
            if (/\s/.test(text[cursor]))
                cursor += 1;
            else if (text[cursor] === "%") {
                while (cursor < text.length && text[cursor] !== "\n")
                    cursor += 1;
            }
            else
                break;
        }
    };
    while (cursor < text.length) {
        whitespace();
        if (cursor === text.length)
            break;
        const start = text.slice(cursor).match(/^@(\w+)\s*([({])/);
        if (!start)
            throw fail();
        cursor += start[0].length;
        const close = start[2] === "{" ? "}" : ")";
        if (start[1].toLowerCase() === "comment") {
            let depth = 1;
            while (cursor < text.length && depth) {
                const char = text[cursor++];
                if (char === "\\") {
                    cursor += 1;
                    continue;
                }
                if (char === start[2])
                    depth += 1;
                if (char === close)
                    depth -= 1;
            }
            if (depth)
                throw fail();
            continue;
        }
        if (/^(string|preamble)$/i.test(start[1])) {
            throw new Error(`BibTeX 暂不支持 @${start[1]} 声明，请导出完整的文献题录`);
        }
        const key = text.slice(cursor).match(/^[^\s,{}()]+\s*,/);
        if (!key)
            throw fail();
        cursor += key[0].length;
        const fields = new Map<string, string>();
        while (true) {
            whitespace();
            if (text[cursor] === close) {
                cursor += 1;
                break;
            }
            const field = text.slice(cursor).match(/^([\w-]+)\s*=\s*/);
            if (!field)
                throw fail();
            cursor += field[0].length;
            let value = "";
            const delimiter = text[cursor];
            if (delimiter === "{" || delimiter === '"') {
                cursor += 1;
                let depth = 1;
                while (cursor < text.length && depth) {
                    const char = text[cursor++];
                    if (char === "\\") {
                        value += char + (text[cursor++] || "");
                        continue;
                    }
                    if (delimiter === "{" && char === "{")
                        depth += 1;
                    if ((delimiter === "{" && char === "}") || (delimiter === '"' && char === '"'))
                        depth -= 1;
                    if (depth)
                        value += char;
                }
                if (depth)
                    throw fail();
            }
            else {
                const token = text.slice(cursor).match(/^(?:\d+|jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)\b/i);
                if (!token)
                    throw fail();
                value = token[0];
                cursor += value.length;
            }
            fields.set(field[1].toLowerCase(), value.replace(/\s+/g, " ").trim());
            whitespace();
            if (text[cursor] === ",")
                cursor += 1;
            else if (text[cursor] !== close)
                throw fail();
        }
        if (!fields.get("title"))
            throw new Error(`BibTeX 第 ${records.length + 1} 条缺少标题，导入已停止`);
        records.push(fields);
    }
    if (!records.length)
        throw new Error("BibTeX 中没有有效文献");
    return records;
}
function draftPaper(partial: Partial<Omit<PaperRecord, 'metadata'>> & {
    metadata: Partial<PaperMetadata> & {
        title: string;
    };
}, source: ImportSource, index: number): PaperRecord {
    const { metadata, ...fields } = partial;
    return createPaperRecord(metadata, { ...fields, id: partial.id || `import-${Date.now()}-${index}`, source });
}
function bibAuthors(value: string): string[] {
    const result: string[] = [];
    let start = 0, depth = 0;
    for (let i = 0; i < value.length; i++) {
        if (value[i] === '\\') {
            i++;
            continue;
        }
        if (value[i] === '{')
            depth++;
        else if (value[i] === '}')
            depth--;
        if (depth === 0 && /^\s+and\s+/i.test(value.slice(i))) {
            const separator = value.slice(i).match(/^\s+and\s+/i)![0];
            result.push(value.slice(start, i).trim());
            i += separator.length - 1;
            start = i + 1;
        }
    }
    result.push(value.slice(start).trim());
    return result.filter(Boolean);
}
export function parseBibTeX(text: string, source: ImportSource): PaperRecord[] {
    return bibRecords(text).map((fields, index) => {
        const year = Number(fields.get("year")) || 0;
        const arxiv = normalizeArxiv(fields.get("eprint") || fields.get("arxiv") || "");
        return draftPaper({
            metadata: {
                title: fields.get("title")!,
                authors: bibAuthors(fields.get("author") || ""),
                venue: fields.get("journal") || fields.get("booktitle") || "未标注",
                year: year,
                identifiers: { doi: fields.get("doi") || undefined, arxiv: arxiv || undefined }
            }
        }, source, index);
    });
}
function bibEscape(value: string) {
    return value.replace(/[{}]/g, "");
}
export function toBibTeX(items: PaperRecord[]) {
    return items.map((item, index) => {
        const doi = item.metadata.identifiers.doi ? `,\n  doi = {${bibEscape(item.metadata.identifiers.doi)}}` : "";
        const eprint = item.metadata.identifiers.arxiv ? `,\n  eprint = {${bibEscape(item.metadata.identifiers.arxiv)}}` : "";
        return `@article{paper${index + 1},\n  title = {${bibEscape(item.metadata.title)}},\n  author = {${item.metadata.authors.map(bibEscape).join(" and ")}},\n  journal = {${bibEscape(item.metadata.venue)}},\n  year = {${item.metadata.year}}${doi}${eprint}\n}`;
    }).join("\n\n");
}
export function parseRIS(text: string, source: ImportSource): PaperRecord[] {
    const records: Map<string, string[]>[] = [];
    let fields: Map<string, string[]> | null = null;
    let lastTag = "";
    text.replace(/^\uFEFF/, "").split(/\r?\n/).forEach((line, lineIndex) => {
        if (!line.trim())
            return;
        const field = line.match(/^([A-Z0-9]{2})\s+-\s*(.*)$/);
        if (!field) {
            if (fields && lastTag && /^\s+/.test(line)) {
                const values = fields.get(lastTag)!;
                values[values.length - 1] += ` ${line.trim()}`;
                return;
            }
            throw new Error(`RIS 第 ${lineIndex + 1} 行格式错误`);
        }
        const [, tag, value] = field;
        if (tag === "TY") {
            if (fields || !value.trim())
                throw new Error(`RIS 第 ${lineIndex + 1} 行记录未闭合或类型为空`);
            fields = new Map();
        }
        if (!fields)
            throw new Error(`RIS 第 ${lineIndex + 1} 行缺少 TY 记录起始标记`);
        if (tag === "ER") {
            records.push(fields);
            fields = null;
            lastTag = "";
            return;
        }
        fields.set(tag, [...(fields.get(tag) || []), value.trim()]);
        lastTag = tag;
    });
    if (fields)
        throw new Error("RIS 记录缺少 ER 结束标记");
    if (!records.length)
        throw new Error("RIS 中没有有效文献");
    return records.map((fields, index) => {
        const title = fields.get("TI")?.[0] || fields.get("T1")?.[0] || "";
        if (!title)
            throw new Error(`RIS 第 ${index + 1} 条缺少标题，导入已停止`);
        const arxiv = normalizeArxiv((fields.get("UR") || []).join(" ") || fields.get("AN")?.[0] || "");
        return draftPaper({
            tags: (fields.get("KW") || []).flatMap((value) => value.split(/[;；]/)).map((tag) => tag.trim()).filter(Boolean),
            metadata: {
                title: title,
                authors: fields.get("AU") || [],
                venue: fields.get("JO")?.[0] || fields.get("T2")?.[0] || "未标注",
                year: Number((fields.get("PY")?.[0] || "").slice(0, 4)) || 0,
                identifiers: { doi: fields.get("DO")?.[0], arxiv: arxiv || undefined }
            }
        }, source, index);
    });
}
export function toRIS(items: PaperRecord[]) {
    return items.map((item) => [
        "TY  - JOUR",
        `TI  - ${item.metadata.title}`,
        ...item.metadata.authors.map((author) => `AU  - ${author}`),
        `JO  - ${item.metadata.venue}`,
        `PY  - ${item.metadata.year}`,
        item.metadata.identifiers.doi ? `DO  - ${item.metadata.identifiers.doi}` : "",
        item.metadata.identifiers.arxiv ? `UR  - https://arxiv.org/abs/${item.metadata.identifiers.arxiv}` : "",
        ...item.tags.map((tag) => `KW  - ${tag}`),
        "ER  - ",
    ].filter(Boolean).join("\n")).join("\n\n");
}
const csvHeaders = ["题名", "作者", "出处", "年份", "DOI", "arXiv", "标签", "文件夹"];
function csvRows(text: string) {
    const rows: string[][] = [];
    let cells: string[] = [];
    let current = "";
    let quoted = false;
    let closedQuote = false;
    const pushCell = () => { cells.push(current.trim()); current = ""; closedQuote = false; };
    for (let index = 0; index < text.length; index += 1) {
        const char = text[index];
        if (char === '"') {
            if (quoted && text[index + 1] === '"') {
                current += '"';
                index += 1;
            }
            else if (quoted) {
                quoted = false;
                closedQuote = true;
            }
            else if (!current.trim() && !closedQuote)
                quoted = true;
            else
                throw new Error(`CSV 第 ${rows.length + 1} 行引号格式错误`);
        }
        else if (char === "," && !quoted) {
            pushCell();
        }
        else if ((char === "\n" || char === "\r") && !quoted) {
            if (char === "\r" && text[index + 1] === "\n")
                index += 1;
            pushCell();
            if (cells.some(Boolean) || cells.length > 1)
                rows.push(cells);
            cells = [];
        }
        else {
            if (closedQuote && char.trim())
                throw new Error(`CSV 第 ${rows.length + 1} 行引号后有无效内容`);
            current += char;
        }
    }
    if (quoted)
        throw new Error(`CSV 第 ${rows.length + 1} 行引号未闭合`);
    pushCell();
    if (cells.some(Boolean) || cells.length > 1)
        rows.push(cells);
    return rows;
}
function csvCell(value: string) {
    return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}
export function parseCSV(text: string, source: ImportSource = "local"): {
    papers: PaperRecord[];
    folderNames: Array<string | null>;
} {
    const rows = csvRows(text.replace(/^\uFEFF/, ""));
    if (rows.length < 2)
        throw new Error("CSV 中没有文献记录");
    const header = rows[0].map((cell) => cell.toLowerCase());
    const indexOf = (...names: string[]) => header.findIndex((cell) => names.includes(cell));
    const titleIndex = indexOf("题名", "title");
    if (titleIndex < 0)
        throw new Error("CSV 缺少 title 或题名列");
    const papers: PaperRecord[] = [];
    const folderNames: Array<string | null> = [];
    rows.slice(1).forEach((cells, index) => {
        if (cells.length !== header.length)
            throw new Error(`CSV 第 ${index + 2} 行列数与表头不一致`);
        const title = cells[titleIndex] || "";
        if (!title)
            throw new Error(`CSV 第 ${index + 2} 行缺少标题，导入已停止`);
        const folder = cells[indexOf("文件夹", "folder")] || "";
        papers.push(draftPaper({
            tags: (cells[indexOf("标签", "tags")] || "").split(/[;；]/).map((tag) => tag.trim()).filter(Boolean),
            metadata: {
                title: title,
                authors: parseAuthorInput(cells[indexOf("作者", "authors")] || "佚名"),
                venue: cells[indexOf("出处", "venue")] || "未标注",
                year: Number(cells[indexOf("年份", "year")]) || 0,
                identifiers: { doi: cells[indexOf("doi")] || undefined, arxiv: normalizeArxiv(cells[indexOf("arxiv")] || "") || undefined }
            }
        }, source, index));
        folderNames.push(folder || null);
    });
    return { papers, folderNames };
}
export function toCSV(items: PaperRecord[], folders: PaperFolder[]) {
    const names = new Map(folders.map((folder) => [folder.id, folder.name]));
    const rows = items.map((item) => [
        item.metadata.title,
        item.metadata.authors.join("; "),
        item.metadata.venue,
        String(item.metadata.year),
        item.metadata.identifiers.doi || "",
        item.metadata.identifiers.arxiv || "",
        item.tags.join(";"),
        item.folderId ? names.get(item.folderId) || "" : "",
    ].map(csvCell).join(","));
    return [csvHeaders.join(","), ...rows].join("\n");
}
export function assignFolders(data: PaperLibraryData, papers: PaperRecord[], folderNames: Array<string | null>, fallbackFolderId: string | null) {
    const folders = [...data.folders];
    const next = papers.map((item, index) => {
        const name = folderNames[index]?.trim();
        if (!name)
            return { ...item, folderId: item.folderId ?? fallbackFolderId };
        let folder = folders.find((entry) => entry.name === name && !entry.parentId);
        if (!folder) {
            folder = { id: `folder-${name}-${folders.length}`, name };
            folders.push(folder);
        }
        return { ...item, folderId: folder.id };
    });
    return { folders, papers: next };
}
