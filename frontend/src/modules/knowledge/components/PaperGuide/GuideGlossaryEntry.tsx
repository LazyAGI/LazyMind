import PaperGuideText, { guideTextSegments } from "./PaperGuideText";

// Split only a short term label, outside math. Keep source offsets in the
// original immutable text so selections still refer to the right passage.
export function glossaryEntry(text: string) {
  const prefix = text.match(/^\s*(?:[•·*-]\s*)?/u)?.[0].length ?? 0;
  for (const segment of guideTextSegments(text)) {
    if (segment.latex !== undefined) continue;
    const match = /[:：]/u.exec(segment.text);
    if (!match) continue;
    const offset = Array.from(text).slice(0, segment.start).join("").length + match.index;
    const term = text.slice(prefix, offset).trimEnd();
    const rest = text.slice(offset + 1);
    const body = rest.trimStart();
    if (!term || term.length > 100 || /[\n。!?！？]/u.test(term) || !body) return undefined;
    return { term, body, termOffset: Array.from(text.slice(0, prefix)).length,
      bodyOffset: Array.from(text.slice(0, text.length - body.length)).length };
  }
}

export default function GuideGlossaryEntry({ text, id }: { text: string; id: string }) {
  const entry = glossaryEntry(text);
  if (!entry) return <p data-guide-text={id}><PaperGuideText text={text}/></p>;
  return <details className="guide-glossary-entry" data-guide-text={id}>
    <summary><PaperGuideText text={entry.term} sourceOffset={entry.termOffset}/><svg className="guide-glossary-chevron" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"><path d="m4 6 4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/></svg></summary>
    <p><PaperGuideText text={entry.body} sourceOffset={entry.bodyOffset}/></p>
  </details>;
}
