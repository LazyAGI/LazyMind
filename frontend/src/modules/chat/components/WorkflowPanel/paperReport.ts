import MarkdownIt from 'markdown-it';

const parser = new MarkdownIt();
type MarkdownToken = { type: string; content: string; info: string; tag: string; map: [number, number] | null };

/** The research text follows the diagrams; the original artifact stays available as source. */
export function paperReportNarrative(source: string): string {
  const tokens = parser.parse(source, {}) as MarkdownToken[];
  const heading = tokens.find((token, index) => token.type === 'heading_open' && token.tag === 'h2'
    && ['研究报告', 'research report'].includes(tokens[index + 1]?.content.trim().toLowerCase()));
  const body = heading ? source.split('\n').slice(heading.map![1]).join('\n').trimStart() : source;
  const bodyTokens = parser.parse(body, {}) as MarkdownToken[];
  const omitted = new Set<number>();
  const omit = (token: MarkdownToken) => {
    if (token.map) for (let line = token.map[0]; line < token.map[1]; line++) omitted.add(line);
  };
  for (let index = 0; index < bodyTokens.length; index++) {
    const token = bodyTokens[index];
    const content = bodyTokens[index + 1]?.content.trim() || '';
    if (token.type === 'heading_open' && /^(调查记录|覆盖缺口|待解决|待查问题|阅读建议|阅读计划|阅读顺序|阅读路线|阅读路线图|阅读导图|Investigation records|Coverage gaps|Open questions|Reading advice|Reading suggestions|Reading recommendations|Reading plan|Reading order|Reading route|Reading route map|Reading map)$/i.test(content)) {
      const next = bodyTokens.slice(index + 1).find(candidate => candidate.type === 'heading_open' && candidate.tag <= token.tag);
      for (let line = token.map![0]; line < (next?.map?.[0] ?? body.split('\n').length); line++) omitted.add(line);
    }
    if (token.type === 'heading_open' && /^(前序调查|同层比较|后续追踪|调查记录|覆盖缺口|待解决|待查问题|Predecessor investigation|Peer investigation|Successor tracking|Coverage gaps)$/i.test(content)) omit(token);
    if (token.type === 'paragraph_open' && /^(待解决[：:]|已完成本轮调查$|仍有待查问题$|本轮查证核实的后续直接方法改动[：:]|以下为待核对的调查记录)/.test(content)) omit(token);
  }
  return body.split('\n').filter((_, index) => !omitted.has(index)).join('\n').replace(/\n{3,}/g, '\n\n').trim();
}

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
