import { guideTextSegments } from "./PaperGuideText";
type Alignment = "left" | "center" | "right";
export interface GuideTable {
  start: number;
  end: number;
  prefix: string;
  suffix: string;
  headers: string[];
  rows: string[][];
  align: Alignment[];
}
/** A deliberately bounded table grammar, not a Markdown/HTML interpreter. */
function cells(line: string): string[] | undefined {
  const value = line.trim();
  if (
    !value.includes("|") ||
    value.includes("`") ||
    guideTextSegments(value).some((s) => s.latex?.includes("|"))
  )
    return;
  const parts: string[] = [];
  let part = "",
    slashes = 0,
    lastWasSeparator = false;
  for (const char of value) {
    if (char === "|" && slashes % 2 === 0) {
      parts.push(part);
      part = "";
      lastWasSeparator = true;
    } else {
      part = char === "|" ? part.slice(0, -1) + "|" : part + char;
      lastWasSeparator = false;
    }
    slashes = char === "\\" ? slashes + 1 : 0;
  }
  parts.push(part);
  if (value.startsWith("|")) parts.shift();
  if (lastWasSeparator) parts.pop();
  return parts.map((p) => p.trim());
}
export function parseGuideTable(text: string): GuideTable | undefined {
  if (text.length > 16000 || /```|~~~/.test(text)) return;
  const lines = Array.from(text.matchAll(/[^\r\n]*(?:\r\n|\n|\r|$)/g))
    .filter((m) => m[0])
    .map((m) => {
      const content = m[0].replace(/(?:\r\n|\n|\r)$/, "");
      return { text: content, start: m.index!, end: m.index! + content.length };
    });
  const separators = lines
    .map((line, index) => ({ index, columns: cells(line.text) }))
    .filter(
      (x) => x.columns?.length && x.columns.every((c) => /^:?-{3,}:?$/.test(c)),
    );
  if (separators.length !== 1) return;
  const { index, columns } = separators[0];
  if (index === 0 || !columns || columns.length > 16) return;
  const headers = cells(lines[index - 1].text);
  if (!headers || headers.length !== columns.length || !headers.some(Boolean))
    return;
  const rows: string[][] = [];
  let last = index;
  for (let i = index + 1; i < lines.length; i++) {
    if (!lines[i].text.trim() || !lines[i].text.includes("|")) break;
    const row = cells(lines[i].text);
    if (!row || row.length !== headers.length || rows.length >= 100) return;
    rows.push(row);
    last = i;
  }
  if (!rows.length) return;
  const begin = lines[index - 1].start,
    end = lines[last].end;
  return {
    start: Array.from(text.slice(0, begin)).length,
    end: Array.from(text.slice(0, end)).length,
    prefix: text.slice(0, begin),
    suffix: text.slice(end),
    headers,
    rows,
    align: columns.map((c) =>
      c.startsWith(":") && c.endsWith(":")
        ? "center"
        : c.endsWith(":")
          ? "right"
          : "left",
    ),
  };
}
