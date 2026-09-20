import { describe, expect, it } from 'vitest';

import type { ConversationArtifact } from '@/modules/chat/store/taskCenter';
import {
  normalizeArtifactSourceType,
  toArtifactFiles,
} from './artifactFiles';

function artifact(partial: Partial<ConversationArtifact> & Pick<ConversationArtifact, 'artifact_id'>): ConversationArtifact {
  return {
    conversation_id: 'c1',
    history_id: 'h1',
    producer_type: 'subagent',
    slot: 'report',
    content_type: 'text',
    seq: 1,
    value: { text: 'hello' },
    ...partial,
  };
}

describe('toArtifactFiles', () => {
  it('classifies user uploads separately from published main-chat files', () => {
    const files = toArtifactFiles([
      artifact({
        artifact_id: 'upload-1',
        source_type: 'user_upload',
        producer_type: 'user',
        filename: 'brief.pdf',
        content_type: 'file',
        value: { url: '/static-files/tmp/brief.pdf' },
      }),
      artifact({
        artifact_id: 'published-1',
        source_type: 'main_chat',
        producer_type: 'main_agent',
        filename: 'summary.md',
      }),
    ]);

    expect(files.map((file) => file.origin)).toEqual(['upload', 'published']);
  });

  it('keeps unpublished main-chat drafts out of the conversation file list', () => {
    const files = toArtifactFiles([
      artifact({
        artifact_id: 'draft-1',
        source_type: 'main_chat',
        producer_type: 'main_agent',
        filename: 'scratch.md',
        publication_status: 'draft',
      }),
      artifact({
        artifact_id: 'published-1',
        source_type: 'main_chat',
        producer_type: 'main_agent',
        filename: 'summary.md',
        publication_status: 'published',
      }),
    ]);
    expect(files.map((file) => file.id)).toEqual(['published-1']);
    expect(files[0].origin).toBe('published');
  });

  it('falls back to producer_type when source_type is missing', () => {
    const files = toArtifactFiles([
      artifact({
        artifact_id: 'chat-1',
        producer_type: 'main_agent',
        filename: 'notes.txt',
      }),
      artifact({
        artifact_id: 'sub-1',
        producer_type: 'subagent',
        slot: 'draft.md',
      }),
    ]);
    expect(files.map((file) => file.sourceType)).toEqual(['main_chat', 'subagent']);
  });

  it('keeps a zip snapshot when file_list value has a url instead of paths', () => {
    const files = toArtifactFiles([
      artifact({
        artifact_id: 'list-1',
        content_type: 'file_list',
        slot: 'outputs',
        value: { url: '/static-files/subagent/artifact-blobs/u1/aa/zip', filename: 'outputs.zip' },
      }),
    ]);
    expect(files).toHaveLength(1);
    expect(files[0]).toMatchObject({
      filename: 'outputs.zip',
      url: '/api/core/static-files/subagent/artifact-blobs/u1/aa/zip',
    });
  });

  it('keeps workflow source_type and revision from projection', () => {
    const files = toArtifactFiles([
      artifact({
        artifact_id: 'wf-1',
        source_type: 'workflow',
        revision: 3,
        seq: 2,
        slot: 'proposal.md',
      }),
    ]);
    expect(files[0]).toMatchObject({
      sourceType: 'workflow',
      revision: 3,
      filename: 'proposal.md',
    });
  });
});

describe('normalizeArtifactSourceType', () => {
  it('maps missing source onto producer', () => {
    expect(normalizeArtifactSourceType(undefined, 'main_agent')).toBe('main_chat');
    expect(normalizeArtifactSourceType(undefined, 'subagent')).toBe('subagent');
  });
});
