import { describe, expect, it } from 'vitest';
import type { RewriteSelectionPreview } from '@/modules/chat/utils/request';
import { mergeMarkdownRewrite, mergeIRRewrite } from './mergeRewritePreview';
import type { WriterDocument } from './writerIR';
import { documentRewritePreview } from './documentRewritePreview';

const source = 'First\n\nKeep\n\nLast';
const preview = { results: [
  { target: { target_start: 0, target_end: 5 }, preview: { old_text: 'First', new_text: 'Clear' } },
  { target: { target_start: 13, target_end: 17 }, preview: { old_text: 'Last', new_text: 'Better' } },
] } as RewriteSelectionPreview;

describe('merging reviewed paragraphs into the current draft', () => {
  it.each([
    [source, 'Clear\n\nKeep\n\nBetter'],
    ['First\n\n**Edited gap**\n\nLast', 'Clear\n\n**Edited gap**\n\nBetter'],
    ['Intro\n\nFirst\n\nKeep\n\nLast\n\nAfter', 'Intro\n\nClear\n\nKeep\n\nBetter\n\nAfter'],
  ])('preserves edits outside the reviewed ranges', (current, expected) => {
    expect(mergeMarkdownRewrite(source, current, preview)).toBe(expected);
  });
  it.each(['Changed\n\nKeep\n\nLast', 'FiXrst\n\nKeep\n\nLast', 'First\n\nKeep'])('refuses changed or deleted review targets', current => {
    expect(() => mergeMarkdownRewrite(source, current, preview)).toThrow();
  });
  it('uses Unicode offsets and distinguishes repeated paragraphs', () => {
    const text = '😀 Same\n\nKeep\n\n😀 Same';
    const result = { results: [{ target: { target_start: 14, target_end: 20 }, preview: { old_text: '😀 Same', new_text: 'Better' } }] } as RewriteSelectionPreview;
    expect(mergeMarkdownRewrite(text, text.replace('Keep', 'Keep changed'), result)).toBe('😀 Same\n\nKeep changed\n\nBetter');
  });
  it('refuses a stale or overlapping server range', () => {
    expect(() => mergeMarkdownRewrite(source, source, { results: [...preview.results!, preview.results![0]] } as RewriteSelectionPreview)).toThrow();
  });
  const original: WriterDocument = { document_id: 'doc', title: 'Title', stage: 'draft', blocks: [
    { node_id: 'one', type: 'paragraph', content: 'First' },
    { node_id: 'gap', type: 'paragraph', content: 'Keep' },
    { node_id: 'last', type: 'paragraph', content: 'Last' },
  ] };
  const irPreview = documentRewritePreview({ representation: 'ir', results: ['one', 'last'].map((id, index) => ({
    target: { type: 'block', block_type: 'paragraph', node_id: id },
    preview: { old_text: index ? 'Last' : 'First', new_text: index ? 'Better' : 'Clear' },
    patch: { type: 'writer_ir_patch', payload: {} },
  })), artifact: { content_type: 'json', value: {
    ...original, blocks: original.blocks.map(block => ({ ...block, content: block.node_id === 'one' ? 'Clear' : block.node_id === 'last' ? 'Better' : 'Keep' })),
  } }, commit: { token: '00000000000000000000000000000001' } }, 3);
  it('preserves other IR text, metadata, formatting and added blocks', () => {
    const current = { ...original, title: 'New title', blocks: [...original.blocks.map(block => block.node_id === 'gap' ? { ...block, content: 'Edited', spans: [{ text: 'Edited', style: ['strong'] }] } : block), { node_id: 'added', type: 'paragraph', content: 'Added' }] };
    const merged = mergeIRRewrite(original, current, irPreview);
    expect(merged.title).toBe('New title');
    expect(merged.blocks.map(block => block.content)).toEqual(['Clear', 'Edited', 'Better', 'Added']);
    expect(merged.blocks[1]).toEqual(current.blocks[1]);
    expect(current.blocks[0].content).toBe('First');
  });
  it.each(['changed', 'deleted', 'readonly'])('refuses %s IR targets', kind => {
    const current = { ...original, blocks: kind === 'deleted' ? original.blocks.slice(1) : original.blocks.map(block => block.node_id === 'one' ? { ...block, ...(kind === 'changed' ? { content: 'Changed' } : { editable: false }) } : block) };
    expect(() => mergeIRRewrite(original, current, irPreview)).toThrow();
  });
  it('accepts unchanged targets after the editor materializes empty children', () => {
    const current = { ...original, blocks: original.blocks.map(block => ({ ...block, children: [], content: block.node_id === 'gap' ? 'Edited' : block.content })) };
    expect(mergeIRRewrite(original, current, irPreview).blocks.map(block => block.content)).toEqual(['Clear', 'Edited', 'Better']);
  });
});
