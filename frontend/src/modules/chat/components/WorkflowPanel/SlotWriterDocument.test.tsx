import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useWorkflowStore, type SlotRevision } from '@/modules/chat/store/workflowPanel';

const workflowApi = vi.hoisted(() => ({
  getSlots: vi.fn(),
  renderWriterDocument: vi.fn(),
  saveWriterDocument: vi.fn(),
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
  MarkdownArtifactEditor: ({
    onSave,
    sourceRevision,
  }: {
    onSave: (markdown: string, revision: number, mode: 'draft') => Promise<unknown>;
    sourceRevision: number;
  }) => (
    <button
      type='button'
      data-source-revision={sourceRevision}
      onClick={() => void onSave('# Edited draft', sourceRevision, 'draft')}
    >
      save markdown draft
    </button>
  ),
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
    change_source: 'ai',
    revision_count: 1,
    version_number: 1,
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
    workflowApi.saveWriterDocument.mockReset();
  });

  it('persists Markdown autosaves as drafts without creating checkpoints', async () => {
    workflowApi.renderWriterDocument.mockResolvedValue(renderedMarkdown('# Initial draft'));
    workflowApi.saveWriterDocument.mockResolvedValue({
      data: {
        code: 0,
        message: 'ok',
        data: {
          title: 'Writer document',
          representation: 'markdown',
          document: '# Edited draft',
          revision: 3,
        },
      },
    });

    render(
      <SlotRenderer
        slot={writerSlot(3)}
        widget={{ widgetType: 'writer-document' }}
        sessionId='writer-session'
        slotId='draft_document'
      />,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'save markdown draft' }));

    await waitFor(() => {
      expect(workflowApi.saveWriterDocument).toHaveBeenCalledWith(
        'writer-session',
        3,
        '# Edited draft',
        'draft_document',
        'draft',
        { silentError: true },
      );
    });
    await waitFor(() => {
      expect(document.querySelector('.workflow-slot__writer-writeback-summary')).toHaveTextContent('草稿');
      expect(document.querySelector('.workflow-slot__writer-writeback-summary')).not.toHaveTextContent('v3');
    });
  });

  it('saves against the selected older revision after rollback', async () => {
    workflowApi.renderWriterDocument.mockResolvedValue(renderedMarkdown('# Selected document'));
    workflowApi.saveWriterDocument.mockResolvedValue({
      data: {
        code: 0,
        message: 'ok',
        data: {
          title: 'Writer document',
          representation: 'markdown',
          document: '# Edited draft',
          revision: 3,
        },
      },
    });
    const getSlotVersions = vi.fn().mockResolvedValue([
      {
        revision: 1,
        version: 1,
        change_source: 'ai',
        created_at: '2026-08-30T03:12:00Z',
        selected: false,
        content_snapshot: '# Initial document',
      },
      {
        revision: 2,
        version: 2,
        change_source: 'ai',
        created_at: '2026-08-30T03:14:03Z',
        selected: true,
        content_snapshot: '# Current document',
      },
    ]);
    const rollbackSlotItem = vi.fn().mockResolvedValue(undefined);
    useWorkflowStore.setState({ getSlotVersions, rollbackSlotItem });

    const { container } = render(
      <SlotRenderer
        slot={writerSlot(2)}
        widget={{ widgetType: 'writer-document' }}
        sessionId='writer-session'
        slotId='draft_document'
        revisionCount={2}
      />,
    );

    const saveButton = await screen.findByRole('button', { name: 'save markdown draft' });
    expect(saveButton).toHaveAttribute('data-source-revision', '2');

    fireEvent.click(container.querySelector<HTMLButtonElement>('.workflow-slot__version-btn')!);
    await waitFor(() => expect(document.querySelectorAll('.workflow-slot__version-item')).toHaveLength(2));
    fireEvent.click(document.querySelectorAll<HTMLElement>('.workflow-slot__version-item')[1]);
    fireEvent.click(document.querySelector<HTMLButtonElement>('.workflow-slot__version-apply-btn')!);

    await waitFor(() => {
      expect(rollbackSlotItem).toHaveBeenCalledWith('writer-session', 'draft_document', -1, 1);
      expect(saveButton).toHaveAttribute('data-source-revision', '1');
    });

    fireEvent.click(saveButton);
    await waitFor(() => {
      expect(workflowApi.saveWriterDocument).toHaveBeenCalledWith(
        'writer-session',
        1,
        '# Edited draft',
        'draft_document',
        'draft',
        { silentError: true },
      );
    });
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

describe('SlotText editing', () => {
  it('keeps the preview footprint and focuses the clicked text', () => {
    const text = '## First line\n\n**middle target**\n\nlast line';
    const targetOffset = text.indexOf('middle target') + 7;
    const slot: SlotRevision = {
      slot_id: 'materials_summary',
      revision: 1,
      selected: true,
      slot: 'materials_summary',
      created_at: '2026-08-31T00:00:00Z',
      artifact_value: { text },
      content_type: 'text',
    };
    const { container } = render(
      <div className='workflow-panel__tab-content'>
        <SlotRenderer
          slot={slot}
          widget={{ widgetType: 'text-markdown' }}
          sessionId='materials-session'
          slotId='materials_summary'
        />
      </div>,
    );
    const scrollContainer = container.querySelector<HTMLElement>('.workflow-panel__tab-content')!;
    const slotElement = container.querySelector<HTMLElement>('.workflow-slot--text')!;
    const preview = container.querySelector<HTMLElement>('.workflow-slot__text--editable')!;
    const renderedTextNode = preview.querySelector('div')!.firstChild!;
    renderedTextNode.textContent = 'middle target';
    scrollContainer.scrollTop = 84;
    vi.spyOn(preview, 'getBoundingClientRect').mockReturnValue({
      x: 10,
      y: 100,
      top: 100,
      left: 10,
      right: 410,
      bottom: 420,
      width: 400,
      height: 320,
      toJSON: () => ({}),
    });
    vi.spyOn(slotElement, 'getBoundingClientRect').mockReturnValue({
      x: 0,
      y: 90,
      top: 90,
      left: 0,
      right: 600,
      bottom: 450,
      width: 600,
      height: 360,
      toJSON: () => ({}),
    });
    Object.defineProperty(document, 'caretPositionFromPoint', {
      configurable: true,
      value: () => ({ offsetNode: renderedTextNode, offset: 7 }),
    });

    fireEvent.click(preview, { clientX: 120, clientY: 240 });

    const editor = container.querySelector<HTMLTextAreaElement>('.workflow-slot__text-editor')!;
    expect(editor).toHaveStyle({ height: '360px', minHeight: '360px' });
    expect(editor.selectionStart).toBe(targetOffset);
    expect(document.activeElement).toBe(editor);
    expect(scrollContainer.scrollTop).toBe(84);
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
        selected: false,
        content_snapshot: '# 工作草稿',
      },
      {
        revision: 3,
        version: 2,
        change_source: 'provider_sync',
        provider_synced: true,
        created_at: '2026-08-27T16:28:04Z',
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
        currentRevision={3}
        currentVersionNumber={2}
        currentValue='# 第二版'
        currentChangeSource='provider_sync'
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

    await waitFor(() => {
      const removed = document.querySelector('.memory-diff-inline-remove');
      const added = document.querySelector('.memory-diff-inline-add');
      expect(removed).toHaveTextContent('初');
      expect(removed?.closest('.memory-diff-line')).toHaveClass('is-remove');
      expect(added).toHaveTextContent('第二');
      expect(added?.closest('.memory-diff-line')).toHaveClass('is-add');
    });

    const versionItems = document.querySelectorAll<HTMLElement>('.workflow-slot__version-item');
    fireEvent.click(versionItems[1]);

    await waitFor(() => {
      expect(document.querySelector('.workflow-slot__version-diff')).toBeNull();
      expect(document.querySelector('.workflow-slot__version-current-text')).toHaveTextContent('# 初版');
    });
    expect(document.querySelector('.workflow-slot__version-apply-btn')).toHaveTextContent('v1');
  });

  it('shows the first manual edit as a draft instead of a numbered version', async () => {
    const getSlotVersions = vi.fn().mockResolvedValue([
      {
        revision: 1,
        version: 1,
        change_source: 'ai',
        created_at: '2026-08-30T03:12:00Z',
        selected: false,
        content_snapshot: '# AI 初稿',
      },
      {
        revision: 2,
        change_source: 'human',
        created_at: '2026-08-30T03:14:03Z',
        selected: true,
        content_snapshot: '# 人工草稿',
      },
    ]);
    useWorkflowStore.setState({ getSlotVersions });

    const { container } = render(
      <SlotVersionPopover
        sessionId='writer-session'
        slotId='draft_document'
        listIndex={-1}
        revisionCount={1}
        currentRevision={2}
        currentVersionNumber={1}
        currentValue='# 人工草稿'
        currentChangeSource='human'
      />,
    );

    const versionButton = container.querySelector<HTMLButtonElement>('.workflow-slot__version-btn')!;
    expect(versionButton).toHaveClass('workflow-slot__version-btn--draft');
    expect(versionButton).not.toHaveTextContent('v2');

    fireEvent.click(versionButton);
    await waitFor(() => expect(document.querySelector('.workflow-slot__version-diff')).not.toBeNull());

    const versionItems = Array.from(document.querySelectorAll('.workflow-slot__version-item'));
    expect(versionItems).toHaveLength(2);
    expect(versionItems[0]).toHaveClass('workflow-slot__version-item--draft');
    expect(versionItems[1]).toHaveTextContent('v1');
    expect(document.querySelector('.workflow-slot__version-popover')).not.toHaveTextContent('v2');
  });

  it('does not show an unchanged final paragraph as a full remove/add pair', async () => {
    const retainedParagraph = '这一段内容没有修改，应该保持为普通上下文。';
    const removedParagraph = '这一句才是实际被删除的内容。';
    const getSlotVersions = vi.fn().mockResolvedValue([
      {
        revision: 1,
        change_source: 'ai',
        created_at: '2026-08-30T03:12:00Z',
        selected: false,
        content_snapshot: `# 标题\n\n${retainedParagraph}\n\n${removedParagraph}`,
      },
      {
        revision: 2,
        change_source: 'human',
        created_at: '2026-08-30T03:14:03Z',
        selected: false,
        content_snapshot: `# 标题\n\n${retainedParagraph}`,
      },
      {
        revision: 3,
        version: 2,
        change_source: 'provider_sync',
        provider_synced: true,
        created_at: '2026-08-30T03:15:03Z',
        selected: true,
        content_snapshot: `# 标题\n\n${retainedParagraph}`,
      },
    ]);
    useWorkflowStore.setState({ getSlotVersions });

    const { container } = render(
      <SlotVersionPopover
        sessionId='writer-session'
        slotId='draft_document'
        listIndex={-1}
        revisionCount={2}
        currentRevision={3}
        currentVersionNumber={2}
        currentValue={`# 标题\n\n${retainedParagraph}`}
        currentChangeSource='provider_sync'
      />,
    );

    fireEvent.click(container.querySelector<HTMLButtonElement>('.workflow-slot__version-btn')!);
    await waitFor(() => expect(document.querySelector('.workflow-slot__version-diff')).not.toBeNull());

    const unchangedLines = Array.from(document.querySelectorAll('.memory-diff-line.is-same'));
    const removedLines = Array.from(document.querySelectorAll('.memory-diff-line.is-remove'));
    const addedLines = Array.from(document.querySelectorAll('.memory-diff-line.is-add'));

    expect(unchangedLines.some((line) => line.textContent?.includes(retainedParagraph))).toBe(true);
    expect(removedLines.some((line) => line.textContent?.includes(retainedParagraph))).toBe(false);
    expect(addedLines.some((line) => line.textContent?.includes(retainedParagraph))).toBe(false);
    expect(removedLines.some((line) => line.textContent?.includes(removedParagraph))).toBe(true);
  });
});
