import { beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkflowSessionApi } from './request';

const { postMock } = vi.hoisted(() => ({
  postMock: vi.fn(),
}));

vi.mock('@/components/request', () => ({
  axiosInstance: {
    defaults: {},
    post: postMock,
  },
  BASE_URL: '',
}));

describe('WorkflowSessionApi.saveWriterDocument', () => {
  beforeEach(() => {
    postMock.mockReset();
  });

  it('omits slot when saving the active draft document', () => {
    WorkflowSessionApi().saveWriterDocument(
      'ps-1',
      12,
      '# Draft',
      'draft_document',
    );

    expect(postMock).toHaveBeenCalledWith(
      '/api/core/workflow-sessions/ps-1/writer-document:save',
      { base_revision: 12, document: '# Draft' },
      undefined,
    );
  });

  it('keeps the explicit slot when saving an outline document', () => {
    WorkflowSessionApi().saveWriterDocument(
      'ps-1',
      3,
      '# Outline',
      'outline_document',
    );

    expect(postMock).toHaveBeenCalledWith(
      '/api/core/workflow-sessions/ps-1/writer-document:save',
      { base_revision: 3, document: '# Outline', slot: 'outline_document' },
      undefined,
    );
  });
});
