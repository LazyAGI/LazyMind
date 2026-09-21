import { diffChars } from 'diff';
import type { RewriteSelectionPreview } from '@/modules/chat/utils/request';
import { findWriterBlock, isWriterDocument, normalizeWriterDocumentForSync, type WriterBlock, type WriterDocument } from './writerIR';

/** Move reviewed ranges past unrelated edits; never replace a changed target. */
export function mergeMarkdownRewrite(source: string, current: string, preview: RewriteSelectionPreview): string {
  return mapMarkdownRewriteRanges(source, current, preview).reverse().reduce((text, item) => text.slice(0, item.start) + item.text + text.slice(item.end), current);
}

export function mapMarkdownRewriteRanges(source: string, current: string, preview: RewriteSelectionPreview) {
  const changes = diffChars(source, current, { timeout: 100 });
  if (!changes || !preview.results?.length) throw new Error('rewrite source unavailable');
  const edits: Array<{ start: number; end: number; length: number }> = [];
  let offset = 0;
  for (const change of changes) {
    if (change.added || change.removed) {
      edits.push({ start: offset, end: offset + (change.removed ? change.value.length : 0), length: change.added ? change.value.length : 0 });
    }
    if (!change.added) offset += change.value.length;
  }
  const points = Array.from(source);
  let previousEnd = 0;
  return preview.results.map(({ target, preview: result }) => {
    const from = target.target_start, to = target.target_end;
    if (!Number.isInteger(from) || !Number.isInteger(to) || from! < previousEnd || to! <= from! || to! > points.length
      || points.slice(from, to).join('') !== result.old_text) throw new Error('rewrite target changed');
    previousEnd = to!;
    const start = points.slice(0, from).join('').length, end = points.slice(0, to).join('').length;
    let shift = 0;
    for (const edit of edits) {
      if ((edit.start < end && edit.end > start) || (edit.start > start && edit.start < end)) throw new Error('rewrite target changed');
      if (edit.end <= start) shift += edit.length - (edit.end - edit.start);
    }
    if (current.slice(start + shift, end + shift) !== result.old_text) throw new Error('rewrite target changed');
    return { start: start + shift, end: end + shift, text: result.new_text };
  });
}

export function mergeIRRewrite(source: WriterDocument, current: WriterDocument, preview: RewriteSelectionPreview): WriterDocument {
  const candidate = preview.artifact.value;
  if (!isWriterDocument(candidate) || !preview.results?.length || current.document_id !== source.document_id) throw new Error('rewrite source unavailable');
  const baseline = normalizeWriterDocumentForSync(source), draft = normalizeWriterDocumentForSync(current);
  // The DOM serializer materializes empty children even on untouched paragraphs.
  const signature = (block: WriterBlock) => JSON.stringify(block, (key, value) => key === 'children' && Array.isArray(value) && !value.length ? undefined : value);
  const replacements = new Map<string, WriterBlock>();
  for (const { target } of preview.results) {
    const id = target.node_id;
    const before = id && findWriterBlock(baseline.blocks, id), now = id && findWriterBlock(draft.blocks, id);
    const after = id && findWriterBlock(candidate.blocks, id);
    if (!id || replacements.has(id) || !before || !now || !after || now.editable === false
      || signature(before) !== signature(now)) throw new Error('rewrite target changed');
    replacements.set(id, after);
  }
  const replace = (blocks: WriterBlock[]): WriterBlock[] => blocks.map(block => replacements.get(block.node_id)
    ?? (block.children ? { ...block, children: replace(block.children) } : block));
  return { ...current, blocks: replace(current.blocks) };
}
