import MarkdownIt from 'markdown-it';

const parser = new MarkdownIt();
type MarkdownToken = { type: string; content: string; info: string; tag: string; map: [number, number] | null };

// This is the publisher's empty-analysis placeholder, not a substantive qualification.
export const visiblePaperAnalysis = (value: unknown): string => typeof value === 'string'
  && value.trim() !== '该项具体结论尚未获得充分正文支持，需进一步核验。' ? value.trim() : '';

export function paperReportSections(source: string): Array<{ title: string; body: string }> {
  const tokens = parser.parse(source, {}) as MarkdownToken[];
  const headings = tokens.filter(token => token.type === 'heading_open' && token.tag === 'h2');
  if (!headings.length) return [];
  const lines = source.split('\n');
  const preface = lines.slice(0, headings[0].map![0]).join('\n').trim();
  return [...(preface ? [{ title: '', body: preface }] : []), ...headings.map((heading, index) => ({
    title: tokens[tokens.indexOf(heading) + 1].content,
    body: lines.slice(heading.map![1], headings[index + 1]?.map?.[0] ?? lines.length).join('\n').trim(),
  }))];
}
