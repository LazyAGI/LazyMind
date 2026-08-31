import { describe, expect, it } from 'vitest';

import {
  applyWriterBlockInternalReference,
  collectWriterReferenceTargets,
  removeWriterBlockInternalReference,
  restoreWriterInternalReferenceDisplayText,
  writerBlockRangeHasInternalReference,
  type WriterDocument,
} from './writerIR';

const document: WriterDocument = {
  document_id: 'writer-doc-1',
  stage: 'draft',
  title: '产品架构说明',
  blocks: [
    {
      node_id: 'sec-1',
      type: 'heading',
      numbering: { level: 1 },
      content: '1 系统设计',
      spans: [{ text: '1 系统设计', style: {} }],
    },
    {
      node_id: 'p-1',
      type: 'paragraph',
      content: '详见第 1 节。',
      spans: [{ text: '详见第 1 节。', style: {} }],
    },
  ],
};

describe('Writer IR cross references', () => {
  it('collects numbered IR blocks as reference targets', () => {
    expect(collectWriterReferenceTargets([
      ...document.blocks,
      {
        node_id: 'image-1',
        type: 'image',
        content: '图1 雨后山间溪流图',
      },
    ])).toEqual([
      { nodeId: 'sec-1', label: '1 系统设计', type: 'heading' },
      { nodeId: 'image-1', label: '图1 雨后山间溪流图', type: 'image' },
    ]);
  });

  it('adds internal_ref styling only to the selected text range', () => {
    const revised = applyWriterBlockInternalReference(document, 'p-1', 2, 7, 'sec-1');
    const paragraph = revised.blocks[1];

    expect(paragraph.spans?.map((span) => span.text).join('')).toBe(paragraph.content);
    expect(paragraph.spans).toEqual([
      { text: '详见', style: {} },
      {
        text: '第 1 节',
        style: {
          link: {
            type: 'internal_ref',
            target_node_id: 'sec-1',
            display_text: '第 1 节',
          },
        },
      },
      { text: '。', style: {} },
    ]);
  });

  it('restores the user-authored label after server materialization', () => {
    const materialized: WriterDocument = {
      ...document,
      blocks: [
        document.blocks[0],
        {
          node_id: 'p-1',
          type: 'paragraph',
          content: '详见第1章。',
          spans: [
            { text: '详见', style: {} },
            {
              text: '第1章',
              style: {
                link: {
                  type: 'internal_ref',
                  target_node_id: 'sec-1',
                  display_text: '这里',
                },
              },
            },
            { text: '。', style: {} },
          ],
        },
      ],
    };

    const restored = restoreWriterInternalReferenceDisplayText(materialized);
    expect(restored.blocks[1].content).toBe('详见这里。');
    expect(restored.blocks[1].spans?.[1].text).toBe('这里');
  });

  it('removes only the internal link while preserving visible text and other styles', () => {
    const materialized: WriterDocument = {
      ...document,
      blocks: [
        document.blocks[0],
        {
          node_id: 'p-1',
          type: 'paragraph',
          content: '详见第1章。',
          spans: [
            { text: '详见', style: {} },
            {
              text: '第1章',
              style: {
                bold: true,
                text_color: 2,
                link: {
                  type: 'internal_ref',
                  target_node_id: 'sec-1',
                  display_text: '这里',
                },
              },
            },
            { text: '。', style: {} },
          ],
        },
      ],
    };

    expect(writerBlockRangeHasInternalReference(materialized.blocks[1], 2, 4)).toBe(true);
    const revised = removeWriterBlockInternalReference(materialized, 'p-1', 2, 4);

    expect(revised.blocks[1].content).toBe('详见这里。');
    expect(revised.blocks[1].spans).toEqual([
      { text: '详见', style: {} },
      { text: '这里', style: { bold: true, text_color: 2 } },
      { text: '。', style: {} },
    ]);
    expect(writerBlockRangeHasInternalReference(revised.blocks[1], 2, 4)).toBe(false);
  });

  it('keeps the display text aligned when only the middle of a reference is removed', () => {
    const materialized: WriterDocument = {
      ...document,
      blocks: [
        document.blocks[0],
        {
          node_id: 'p-1',
          type: 'paragraph',
          content: '详见第1章。',
          spans: [
            { text: '详见', style: {} },
            {
              text: '第1章',
              style: {
                bold: true,
                link: {
                  type: 'internal_ref',
                  target_node_id: 'sec-1',
                  display_text: '这里呀',
                },
              },
            },
            { text: '。', style: {} },
          ],
        },
      ],
    };

    const revised = removeWriterBlockInternalReference(materialized, 'p-1', 3, 4);

    expect(revised.blocks[1].content).toBe('详见这里呀。');
    expect(revised.blocks[1].spans).toEqual([
      { text: '详见', style: {} },
      {
        text: '这',
        style: {
          bold: true,
          link: {
            type: 'internal_ref',
            target_node_id: 'sec-1',
            display_text: '这',
          },
        },
      },
      { text: '里', style: { bold: true } },
      {
        text: '呀',
        style: {
          bold: true,
          link: {
            type: 'internal_ref',
            target_node_id: 'sec-1',
            display_text: '呀',
          },
        },
      },
      { text: '。', style: {} },
    ]);
    expect(writerBlockRangeHasInternalReference(revised.blocks[1], 2, 3)).toBe(true);
    expect(writerBlockRangeHasInternalReference(revised.blocks[1], 3, 4)).toBe(false);
    expect(writerBlockRangeHasInternalReference(revised.blocks[1], 4, 5)).toBe(true);
  });

  it('returns the same document when the selected text is not a cross-reference', () => {
    expect(removeWriterBlockInternalReference(document, 'p-1', 2, 7)).toBe(document);
  });
});
