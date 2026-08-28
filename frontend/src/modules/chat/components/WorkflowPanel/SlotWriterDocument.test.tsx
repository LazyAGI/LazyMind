import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useWorkflowStore, type SlotRevision } from '@/modules/chat/store/workflowPanel';

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

import { resolveSnapshotDiffText, SlotRenderer, SlotVersionPopover } from './SlotComponents';

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
        widget={{ widgetType: 'writer-document' }}
        sessionId='writer-session'
        slotId='draft_document'
        readOnly
      />,
    );
    await waitFor(() => expect(workflowApi.renderWriterDocument).toHaveBeenCalledTimes(1));

    rerender(
      <SlotRenderer
        slot={writerSlot(2)}
        widget={{ widgetType: 'writer-document' }}
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

describe('Writer version diff text', () => {
  it('compares JSON-encoded Writer snapshots as readable document content', async () => {
    const snapshot = JSON.stringify({
      document_id: 'writer-document-1',
      stage: 'final',
      title: '测试文档',
      blocks: [
        {
          node_id: 'heading-1',
          type: 'heading',
          content: '第一章',
          numbering: { level: 1 },
          children: [],
          provider_payload: { raw_block: { internal: 'must not enter the diff' } },
        },
        {
          node_id: 'paragraph-1',
          type: 'paragraph',
          content: '正文内容',
          children: [],
          provider_payload: { source_index: 42 },
        },
      ],
    });

    await expect(resolveSnapshotDiffText(snapshot)).resolves.toBe(
      '# 测试文档\n\n## 第一章\n\n正文内容',
    );
  });

  it('compares each revision with its predecessor and previews the first revision', async () => {
    const getSlotVersions = vi.fn().mockResolvedValue([
      {
        revision: 1,
        change_source: 'ai',
        created_at: '2026-08-27T15:58:05Z',
        selected: false,
        content_snapshot: '# 初版',
      },
      {
        revision: 2,
        change_source: 'human',
        created_at: '2026-08-27T16:26:04Z',
        selected: true,
        content_snapshot: '# 第二版',
      },
    ]);
    useWorkflowStore.setState({ getSlotVersions });

    const { container } = render(
      <SlotVersionPopover
        sessionId='writer-session'
        slotId='draft_document'
        listIndex={-1}
        revisionCount={2}
        currentRevision={2}
        currentValue='# 第二版'
      />,
    );

    fireEvent.click(container.querySelector<HTMLButtonElement>('.workflow-slot__version-btn')!);
    await waitFor(() => expect(document.querySelector('.workflow-slot__version-diff')).not.toBeNull());

    const labels = document.querySelectorAll('.workflow-slot__version-diff-label');
    expect(labels[0]).toHaveTextContent('v1');
    expect(labels[1]).toHaveTextContent('v2');
    expect(document.querySelector('.workflow-slot__version-diff-header')).toHaveTextContent('修改前');
    expect(document.querySelector('.workflow-slot__version-diff-header')).toHaveTextContent('修改后');
    expect(document.querySelector('.workflow-slot__version-diff-arrow')).toHaveTextContent('→');
    expect(document.querySelector('.workflow-slot__version-diff')).not.toHaveTextContent('当前版本');

    const versionItems = document.querySelectorAll<HTMLElement>('.workflow-slot__version-item');
    fireEvent.click(versionItems[1]);

    await waitFor(() => {
      expect(document.querySelector('.workflow-slot__version-diff')).toBeNull();
      expect(document.querySelector('.workflow-slot__version-current-text')).toHaveTextContent('# 初版');
    });
    expect(document.querySelector('.workflow-slot__version-apply-btn')).toHaveTextContent('v1');
  });
});
