import { describe, expect, it } from 'vitest';

import {
  parseWriterDocument,
  restoreLegacyWriterImageReference,
  type WriterDocument,
} from './writerIR';

describe('parseWriterDocument tables', () => {
  it('accepts the canonical tree and rejects content-only tables', () => {
    const canonical: WriterDocument = {
      document_id: 'document-1',
      stage: 'draft',
      title: 'Document',
      blocks: [{
        node_id: 'table-1',
        type: 'table',
        children: [{
          node_id: 'row-1',
          type: 'table_row',
          children: [{ node_id: 'cell-1', type: 'table_cell', content: '' }],
        }],
      }],
    };

    expect(parseWriterDocument(canonical).ok).toBe(true);
    expect(parseWriterDocument({
      ...canonical,
      blocks: [{ node_id: 'table-1', type: 'table', content: '| A |' }],
    }).issues).toContain('blocks[0] must contain only non-empty table_row children');
  });
});

describe('restoreLegacyWriterImageReference', () => {
  it('matches a Feishu source image to its persisted input resource', () => {
    const document: WriterDocument = {
      document_id: 'document-1',
      stage: 'draft',
      title: 'Document',
      blocks: [{
        node_id: 'image-1',
        type: 'image',
        provider_binding: { provider: 'feishu', block_id: 'block-1' },
        provider_payload: { raw_block: { image: { token: 'token-1' } } },
      }],
    };
    const restored = restoreLegacyWriterImageReference(document, {
      assets: {
        'asset-1': {
          media_asset_id: 'asset-1',
          source_type: 'input_resource',
          uri: 'https://example.feishu.cn/docx/document-1#image=block-1',
          local_path: '/data/subagent/task-1/media/source.png',
          meta: {
            provider: 'feishu',
            provider_block_id: 'block-1',
            origin: 'source_document',
          },
        },
      },
    });

    expect(restored.blocks[0].references).toContainEqual({
      type: 'media_asset',
      id: 'asset-1',
      path: '/data/subagent/task-1/media/source.png',
    });
  });

});
