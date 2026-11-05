import type { PdfIRBlock } from '@/modules/knowledge/api/pdfIR';

export function formulaNumber(block: PdfIRBlock): string | undefined {
  const tags = [...(block.latex || '').matchAll(/\\tag\*?\{\s*([\d]+(?:\.\d+)*[a-z]?)\s*\}/gi)];
  return tags.length === 1 ? tags[0][1].toLowerCase() : undefined;
}
export function formulaNumberMap(blocks: PdfIRBlock[]) {
  const map = new Map<string, PdfIRBlock>();
  const ambiguous = new Set<string>();
  for (const block of blocks) {
    if (block.type !== 'formula') continue;
    const number = formulaNumber(block);
    if (!number) continue;
    if (map.has(number)) ambiguous.add(number);
    map.set(number, block);
  }
  ambiguous.forEach(number => map.delete(number));
  return map;
}
// Ranges preserve the native PDF.js selection and link layers. Read geometry at
// interaction time so browser zoom and page resizing cannot stale the hit boxes.
export function referenceAtPoint(page: HTMLElement, formulas: Map<string, PdfIRBlock>, x: number, y: number) {
  const spans = Array.from(page.querySelectorAll<HTMLElement>('.react-pdf__Page__textContent span'));
  const nodes: Array<{node:Text; start:number; end:number}> = [];
  let text = '';
  for (const span of spans) {
    for (const node of Array.from(span.childNodes)) {
      if (node.nodeType !== Node.TEXT_NODE) continue;
      const value = node.textContent || '';
      nodes.push({node:node as Text,start:text.length,end:text.length+value.length});
      text += value;
    }
  }
  const pattern = /(?:\bEq\.?|\bEquation|式)\s*\(\s*(\d+(?:\.\d+)*[a-z]?)\s*\)/gi;
  for (const match of text.matchAll(pattern)) {
    const formula = formulas.get(match[1].toLowerCase());
    if (!formula) continue;
    const start = match.index!, end = start + match[0].length;
    const first = nodes.find(n => n.start <= start && n.end > start);
    const last = nodes.find(n => n.start < end && n.end >= end);
    if (!first || !last) continue;
    const range = document.createRange();
    range.setStart(first.node, start-first.start);
    range.setEnd(last.node, end-last.start);
    if (Array.from(range.getClientRects()).some(r=>x>=r.left && x<=r.right && y>=r.top && y<=r.bottom)) return formula;
  }
}

export function formulaReferenceForClick(page:HTMLElement, formulas:Map<string,PdfIRBlock>, target:Element, start:{x:number;y:number}|undefined, x:number, y:number, selection:string) {
  if (!start || selection || Math.hypot(x-start.x,y-start.y)>4 || target.closest('button,textarea,input,select')) return;
  const anchor=target.closest('a');
  // PDF.js marks the containing annotation section, not the anchor itself.
  // Never intercept external links or unrelated native destination links.
  if (anchor && !anchor.closest('[data-internal-link]')) return;
  return referenceAtPoint(page,formulas,x,y);
}
