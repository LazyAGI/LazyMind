import MarkdownIt from 'markdown-it';

const parser = new MarkdownIt();
type MarkdownToken = { type: string; content: string; info: string; tag: string; map: [number, number] | null };

/** Read diagrams from existing Markdown; no inferred edges or additional persisted fields. */
export function paperReportDiagrams(source: string) {
  const diagrams: { relations?: string; reading?: string } = {};
  let section: keyof typeof diagrams | undefined;
  const tokens = parser.parse(source, {}) as MarkdownToken[];
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    if (token.type === 'heading_open') {
      const title = tokens[index + 1]?.content.trim().toLowerCase();
      section = title === '论文关系图' || title === 'paper relationships' ? 'relations'
        : ['阅读导图', '阅读路线图', 'reading map'].includes(title) ? 'reading' : undefined;
    }
    if (section && token.type === 'fence' && token.info.trim() === 'mermaid' && token.content.trim()) {
      diagrams[section] ??= token.content;
    }
  }
  return diagrams;
}

/** The research text follows the diagrams; the original artifact stays available as source. */
export function paperReportNarrative(source: string): string {
  const tokens = parser.parse(source, {}) as MarkdownToken[];
  const heading = tokens.find((token, index) => token.type === 'heading_open' && token.tag === 'h2'
    && ['研究报告', 'research report'].includes(tokens[index + 1]?.content.trim().toLowerCase()));
  return heading ? source.split('\n').slice(heading.map![0]).join('\n') : source;
}

export function paperRelationEvidence(source: string): string {
  const tokens = parser.parse(source, {}) as MarkdownToken[];
  const lines = source.split('\n');
  const index = tokens.findIndex((token, i) => token.type === 'heading_open'
    && ['论文关系图', 'paper relationships'].includes(tokens[i + 1]?.content.trim().toLowerCase()));
  if (index < 0) return '';
  const heading = tokens[index];
  const start = heading.map![1];
  const next = tokens.slice(index + 1).find(token => token.type === 'heading_open' && token.tag <= heading.tag);
  const end = next?.map?.[0] ?? lines.length;
  const omitted = new Set<number>();
  for (const token of tokens) if (token.type === 'fence' && token.info.trim() === 'mermaid' && token.map
    && token.map[0] >= start && token.map[0] < end) {
    for (let line = token.map[0]; line < token.map[1]; line++) omitted.add(line);
  }
  return lines.slice(start, end).filter((_, index) => !omitted.has(start + index)).join('\n').trim();
}

// This is the publisher's empty-analysis placeholder, not a substantive qualification.
export const visiblePaperAnalysis = (value: unknown): string => typeof value === 'string'
  && value.trim() !== '该项具体结论尚未获得充分正文支持，需进一步核验。' ? value.trim() : '';
