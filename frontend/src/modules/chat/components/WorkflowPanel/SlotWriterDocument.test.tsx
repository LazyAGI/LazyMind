import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';

const workflowApi = vi.hoisted(() => ({
  getSlots: vi.fn(),
  renderWriterDocument: vi.fn(),
}));

vi.mock('@/modules/chat/utils/request', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => workflowApi,
}));

vi.mock('@/modules/chat/components/MarkdownViewer', () => ({
  default: ({ children }: { children: string }) => <div>{children}</div>,
}));

vi.mock('./FilePreviewDrawer', () => ({
  FilePreviewDrawer: () => null,
}));

vi.mock('./MarkdownArtifactEditor', () => ({
  MarkdownArtifactEditor: () => null,
}));

vi.mock('./WriterDownloadFormat', () => ({
  WriterDownloadFormatButton: () => null,
  WriterDownloadFormatDialog: () => null,
  writerDownloadCacheKey: () => '',
  writerDownloadFilename: () => '',
  writerMarkdownTitle: () => '',
}));

import { SlotRenderer } from './SlotComponents';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function writerSlot(revision: number): SlotRevision {
  return {
    slot_id: 'draft_document',
    revision,
    selected: true,
    slot: 'draft_document',
    created_at: '2026-08-18T00:00:00Z',
    artifact_value: { path: 'draft_document.lmd' },
  };
}

function renderedMarkdown(document: string) {
  return {
    data: {
      code: 0,
      message: 'ok',
      data: {
        title: 'Writer document',
        representation: 'markdown',
        document,
      },
    },
  };
}

describe('SlotWriterDocument render refresh', () => {
  beforeEach(() => {
    workflowApi.getSlots.mockReset();
    workflowApi.getSlots.mockResolvedValue({ data: { data: { slots: [] } } });
    workflowApi.renderWriterDocument.mockReset();
  });

  it('does not let a canceled stale request replace the latest successful render', async () => {
    const staleRequest = deferred<ReturnType<typeof renderedMarkdown>>();
    const latestRequest = deferred<ReturnType<typeof renderedMarkdown>>();
    workflowApi.renderWriterDocument
      .mockReturnValueOnce(staleRequest.promise)
      .mockReturnValueOnce(latestRequest.promise);

    const { rerender } = render(
      <SlotRenderer
        slot={writerSlot(1)}
        sessionId='writer-session'
        slotId='draft_document'
        readOnly
      />,
    );
    await waitFor(() => expect(workflowApi.renderWriterDocument).toHaveBeenCalledTimes(1));

    rerender(
      <SlotRenderer
        slot={writerSlot(2)}
        sessionId='writer-session'
        slotId='draft_document'
        readOnly
      />,
    );
    await waitFor(() => expect(workflowApi.renderWriterDocument).toHaveBeenCalledTimes(2));

    await act(async () => {
      latestRequest.resolve(renderedMarkdown('# latest document'));
      await latestRequest.promise;
    });
    expect(screen.getByText('# latest document')).toBeInTheDocument();

    await act(async () => {
      staleRequest.reject(Object.assign(new Error('canceled'), {
        code: 'ERR_CANCELED',
        name: 'CanceledError',
      }));
      await staleRequest.promise.catch(() => undefined);
    });

    expect(screen.getByText('# latest document')).toBeInTheDocument();
    expect(document.querySelector('.workflow-slot--error')).not.toBeInTheDocument();
  });
});
