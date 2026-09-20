import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { draftStore, useWorkflowStore, type SlotRevision } from '@/modules/chat/store/workflowPanel';

const workflowApi = vi.hoisted(() => ({
  getSlots: vi.fn(),
  renderWriterDocument: vi.fn(),
  saveWriterDocument: vi.fn(),
  writeBackWriterDocument: vi.fn(),
}));
const modalConfirm = vi.hoisted(() => vi.fn());
const markdownEditorRender = vi.hoisted(() => vi.fn());
const chunkUpload = vi.hoisted(() => ({ uploadFileInChunks: vi.fn() }));

vi.mock('@/modules/chat/utils/request', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => workflowApi,
}));

vi.mock('@/modules/chat/components/MarkdownViewer', () => ({
  default: ({ children }: { children: string }) => <div>{children}</div>,
}));

vi.mock('@/modules/chat/utils/chunkUpload', () => chunkUpload);

vi.mock('antd', async (importOriginal) => {
  const actual = await importOriginal<typeof import('antd')>();
  return { ...actual, Modal: { ...actual.Modal, confirm: modalConfirm } };
});

vi.mock('./FilePreviewDrawer', () => ({
  FilePreviewDrawer: () => null,
}));

vi.mock('./MarkdownArtifactEditor', () => ({
  MarkdownArtifactEditor: (props: {
    markdown: string;
    resolveImageUrl?: (url: string) => Promise<string>;
    onSave: (markdown: string, revision: number, mode: 'draft') => Promise<unknown>;
    sourceRevision: number;
    maxHeight?: number;
    editingKey?: string;
    onRewritePreviewApplied?: (revision?: number, draftVersion?: number) => void;
  }) => {
    markdownEditorRender(props);
    const {
      markdown,
      onSave,
      sourceRevision,
      maxHeight,
      editingKey,
    } = props;
    return (
      <>
        <button
          type='button'
          data-markdown={markdown}
          data-source-revision={sourceRevision}
          data-max-height={maxHeight}
          data-editing-key={editingKey}
          onClick={() => void onSave('# Edited draft', sourceRevision, 'draft')}
        >
          save markdown draft
        </button>
        {props.onRewritePreviewApplied && (
          <button type='button' onClick={() => props.onRewritePreviewApplied?.(sourceRevision, 5)}>
            apply rewrite baseline
          </button>
        )}
      </>
    );
  },
}));

vi.mock('./WriterDownloadFormat', () => ({
  WriterDownloadFormatButton: () => null,
  WriterDownloadFormatDialog: () => null,
  writerDownloadCacheKey: () => '',
  writerDownloadFilename: () => '',
  writerMarkdownTitle: () => '',
}));

import { resolveSnapshotDiffText, SlotEditingContext, SlotRenderer, SlotVersionPopover } from './SlotComponents';
import { WriterProviderChoice } from './SlotComponents';
import type { SlotFooterAction } from './slotEditingContext';

afterEach(() => vi.unstubAllGlobals());

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

function writerSourceSlot(): SlotRevision {
  return {
    slot_id: 'source_document',
    revision: 1,
    selected: true,
    slot: 'source_document',
    created_at: '2026-09-04T00:00:00Z',
    content_type: 'file',
    artifact_value: { filename: 'source.md', url: 'https://example.test/source.md' },
    editor_profile: 'writer-markdown-source',
  };
}

function renderedMarkdown(document: string, media_urls?: Record<string, string>) {
  return {
    data: {
      code: 0,
      message: 'ok',
      data: {
        title: 'Writer document',
        representation: 'markdown',
        document,
        numbering: { ordered_style: 'hierarchical', entries: {} },
        media_urls,
      },
    },
  };
}

describe('Writer write-back provider choice', () => {
  it('shows the Obsidian logo and falls back when it cannot be loaded', () => {
    const { container } = render(
      <WriterProviderChoice
        initialProvider='obsidian'
        githubEnabled={false}
        onChange={vi.fn()}
      />,
    );

    const obsidian = container.querySelector<HTMLInputElement>('input[value="obsidian"]')!;
    const option = obsidian.closest('label')!;
    const logo = option.querySelector<HTMLImageElement>(
      'img[src="https://obsidian.md/images/obsidian-logo-gradient.svg"]',
    )!;
    expect(logo).toBeInTheDocument();

    fireEvent.error(logo);
    expect(option.querySelector('.anticon-folder-open')).toBeInTheDocument();
  });

  it('selects GitHub only after the conversation binds a target', () => {
    const onChange = vi.fn();
    const { container, rerender } = render(
      <WriterProviderChoice
        initialProvider='feishu'
        githubEnabled={false}
        onChange={onChange}
      />,
    );

    expect(container.querySelector<HTMLInputElement>('input[value="github"]')).toBeDisabled();
    const wechat = container.querySelector<HTMLInputElement>('input[value="wechat"]')!;
    expect(wechat).toBeEnabled();
    fireEvent.click(wechat);
    expect(onChange).toHaveBeenCalledWith('wechat');

    rerender(
      <WriterProviderChoice
        initialProvider='feishu'
        githubEnabled
        onChange={onChange}
      />,
    );
    const github = container.querySelector<HTMLInputElement>('input[value="github"]')!;
    expect(github).toBeEnabled();

    fireEvent.click(github);
    expect(onChange).toHaveBeenCalledWith('github');
  });
});

describe('SlotWriterDocument render refresh', () => {
  beforeEach(() => {
    workflowApi.getSlots.mockReset();
    workflowApi.getSlots.mockResolvedValue({ data: { data: { slots: [] } } });
    workflowApi.renderWriterDocument.mockReset();
    workflowApi.saveWriterDocument.mockReset();
    markdownEditorRender.mockReset();
    chunkUpload.uploadFileInChunks.mockReset();
    chunkUpload.uploadFileInChunks.mockResolvedValue('/var/lib/lazymind/uploads/source.md');
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
          numbering: { ordered_style: 'hierarchical', entries: {} },
          revision: 3,
          draft_version: 5,
        },
      },
    });

    render(
      <SlotRenderer
        slot={{ ...writerSlot(3), change_source: 'human', draft_version: 4 }}
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
        4,
        '# Edited draft',
        'draft_document',
        'draft',
        undefined,
        { silentError: true },
      );
    });
    await waitFor(() => {
      expect(document.querySelector('.workflow-slot__writer-writeback-summary')).toHaveTextContent('草稿');
      expect(document.querySelector('.workflow-slot__writer-writeback-summary')).not.toHaveTextContent('v3');
    });
  });

  it('shows a persisted local target path after refresh', async () => {
    workflowApi.renderWriterDocument.mockResolvedValue(renderedMarkdown('# Obsidian note'));
    const slot = {
      ...writerSlot(2),
      provider: 'obsidian',
      change_source: 'provider_sync' as const,
      write_back_ready: true,
      write_back_state: 'synced_clean' as const,
      write_back_local_path: '/Users/test/Documents/obs/Note.md',
    };

    render(
      <SlotRenderer
        slot={slot}
        widget={{ widgetType: 'writer-document' }}
        sessionId='writer-session'
        slotId='draft_document'
      />,
    );

    fireEvent.click(await screen.findByRole('link', { name: '打开云文档' }));
    expect(await screen.findByText('/Users/test/Documents/obs/Note.md')).toBeInTheDocument();
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
          numbering: { ordered_style: 'hierarchical', entries: {} },
          revision: 3,
          draft_version: 1,
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
        undefined,
        '# Edited draft',
        'draft_document',
        'draft',
        undefined,
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

  it('uses the server-selected source profile for media and draft saves', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      text: async () => '# Original',
    }));
    workflowApi.renderWriterDocument.mockResolvedValue(renderedMarkdown('# Original', {
      '_assets/logo.png': 'https://example.test/signed-logo.png',
    }));
    const patchSlotItemValue = vi.fn()
      .mockResolvedValueOnce(2)
      .mockResolvedValueOnce(3);
    useWorkflowStore.setState({ patchSlotItemValue });

    const { rerender } = render(
      <SlotRenderer
        slot={writerSourceSlot()}
        expectedType='file'
        sessionId='writer-session'
        slotId='source_document'
      />,
    );

    await screen.findByRole('button', { name: 'save markdown draft' });
    const editorProps = markdownEditorRender.mock.calls[markdownEditorRender.mock.calls.length - 1]?.[0];
    await expect(editorProps.resolveImageUrl('_assets/logo.png')).resolves.toBe(
      'https://example.test/signed-logo.png',
    );

    await act(() => editorProps.onSave('# Original', 1, 'draft'));
    expect(patchSlotItemValue).not.toHaveBeenCalled();
    await act(() => editorProps.onSave('# Edited draft', 1, 'draft'));
    expect(patchSlotItemValue).toHaveBeenCalledWith(
      'writer-session', 'source_document', -1,
      expect.objectContaining({ document_format: 'markdown' }),
      'file', 'draft', 1, undefined,
    );

    const rendersBeforeBaselineRefresh = markdownEditorRender.mock.calls.length;
    rerender(
      <SlotRenderer
        slot={{
          ...writerSourceSlot(),
          revision: 2,
          draft_version: 1,
          change_source: 'human',
        }}
        expectedType='file'
        sessionId='writer-session'
        slotId='source_document'
      />,
    );
    const refreshedEditorProps = await waitFor(() => {
      expect(markdownEditorRender.mock.calls.length).toBeGreaterThan(rendersBeforeBaselineRefresh);
      const props = markdownEditorRender.mock.calls[markdownEditorRender.mock.calls.length - 1]?.[0];
      expect(props.sourceRevision).toBe(2);
      return props;
    });
    await act(() => refreshedEditorProps.onSave('# Edited draft', 2, 'checkpoint'));
    expect(patchSlotItemValue).toHaveBeenNthCalledWith(
      2,
      'writer-session', 'source_document', -1,
      expect.objectContaining({ document_format: 'markdown' }),
      'file', 'checkpoint', 2, 1,
    );
  });
});

describe('SlotText editing', () => {
  it('marks json-block slots as their own scroll container', () => {
    const slot: SlotRevision = {
      slot_id: 'generation_parameters',
      revision: 1,
      selected: true,
      slot: 'generation_parameters',
      created_at: '2026-09-01T00:00:00Z',
      artifact_value: {
        count_unit: 'chinese_characters',
        paper_type: 'research',
        word_target: 1000,
      },
      content_type: 'json',
    };

    const { container } = render(
      <SlotRenderer
        slot={slot}
        widget={{ widgetType: 'json-block', readOnly: true, collapsed: false }}
        sessionId='academic-session'
        slotId='generation_parameters'
        readOnly
      />,
    );

    expect(container.querySelector('.workflow-slot--json-block')).toBeInTheDocument();
    expect(screen.getByText(/chinese_characters/)).toBeInTheDocument();
  });

  it('reuses the Markdown editor and saves through the text-slot revision contract', async () => {
    const patchSlotItemValue = vi.fn().mockResolvedValue(1);
    useWorkflowStore.setState({ patchSlotItemValue });
    const slot: SlotRevision = {
      slot_id: 'materials_summary',
      revision: 1,
      selected: true,
      slot: 'materials_summary',
      created_at: '2026-08-31T00:00:00Z',
      artifact_value: { text: '# Initial Markdown' },
      content_type: 'text',
      change_source: 'human',
      draft_version: 4,
    };

    const { rerender } = render(
      <SlotRenderer
        slot={slot}
        widget={{ widgetType: 'text-markdown', maxHeight: 680 }}
        sessionId='materials-session'
        slotId='materials_summary'
      />,
    );

    const editor = screen.getByRole('button', { name: 'save markdown draft' });
    expect(editor).toHaveAttribute('data-markdown', '# Initial Markdown');
    expect(editor).toHaveAttribute('data-source-revision', '1');
    expect(editor).toHaveAttribute('data-max-height', '680');
    expect(editor).toHaveAttribute(
      'data-editing-key',
      'materials-session:materials_summary:-1:markdown',
    );

    fireEvent.click(editor);

    await waitFor(() => {
      expect(patchSlotItemValue).toHaveBeenCalledWith(
        'materials-session',
        'materials_summary',
        -1,
        { text: '# Edited draft' },
        'text',
        'draft',
        1,
        4,
      );
      expect(editor).toHaveAttribute('data-source-revision', '1');
    });

    rerender(
      <SlotRenderer
        slot={{ ...slot, artifact_value: { text: '# Edited draft' }, draft_version: 5 }}
        widget={{ widgetType: 'text-markdown', maxHeight: 680 }}
        sessionId='materials-session'
        slotId='materials_summary'
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'save markdown draft' }));
    await waitFor(() => {
      expect(patchSlotItemValue).toHaveBeenNthCalledWith(
        2,
        'materials-session',
        'materials_summary',
        -1,
        { text: '# Edited draft' },
        'text',
        'draft',
        1,
        5,
      );
    });
  });

  it('uses the applied rewrite draft version for the next save without remounting', async () => {
    const patchSlotItemValue = vi.fn().mockResolvedValue(1);
    useWorkflowStore.setState({ patchSlotItemValue });
    const slot: SlotRevision = {
      slot_id: 'materials_summary',
      revision: 1,
      selected: true,
      slot: 'materials_summary',
      created_at: '2026-09-10T00:00:00Z',
      artifact_value: { text: '# Initial Markdown' },
      content_type: 'text',
      change_source: 'human',
      draft_version: 4,
    };
    render(
      <SlotRenderer
        slot={slot}
        widget={{ widgetType: 'text-markdown' }}
        sessionId='materials-session'
        slotId='materials_summary'
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'apply rewrite baseline' }));
    fireEvent.click(screen.getByRole('button', { name: 'save markdown draft' }));

    await waitFor(() => expect(patchSlotItemValue).toHaveBeenCalledWith(
      'materials-session', 'materials_summary', -1,
      { text: '# Edited draft' }, 'text', 'draft', 1, 5,
    ));
  });

  it('keeps the preview footprint and focuses the clicked plain text', () => {
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
          widget={{ widgetType: 'text-single' }}
          sessionId='materials-session'
          slotId='materials_summary'
        />
      </div>,
    );
    const scrollContainer = container.querySelector<HTMLElement>('.workflow-panel__tab-content')!;
    const slotElement = container.querySelector<HTMLElement>('.workflow-slot--text')!;
    const preview = container.querySelector<HTMLElement>('.workflow-slot__text--editable')!;
    const renderedTextNode = preview.firstChild!;
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

describe('SlotImage replacement', () => {
  it('uses the refreshed revision and draft-version baseline on consecutive replacements', async () => {
    class ReadyImage {
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;

      set src(_value: string) {
        queueMicrotask(() => this.onload?.());
      }
    }
    vi.stubGlobal('Image', ReadyImage);
    const patchSlotItemValue = vi.fn().mockResolvedValue(2);
    useWorkflowStore.setState({ patchSlotItemValue });
    chunkUpload.uploadFileInChunks.mockReset();
    chunkUpload.uploadFileInChunks
      .mockResolvedValueOnce('/var/lib/lazymind/uploads/first.png')
      .mockResolvedValueOnce('/var/lib/lazymind/uploads/second.png');
    const slot: SlotRevision = {
      slot_id: 'images',
      revision: 1,
      list_index: 0,
      selected: true,
      slot: 'images',
      created_at: '2026-09-10T00:00:00Z',
      content_type: 'image',
      artifact_value: { url: 'https://example.test/original.png' },
      change_source: 'ai',
    };
    const { container, rerender } = render(
      <SlotRenderer
        slot={slot}
        expectedType='image'
        cardMode
        sessionId='image-session'
        slotId='images'
      />,
    );

    const replace = async (name: string) => {
      const input = await waitFor(() => {
        const element = container.querySelector<HTMLInputElement>('input[type="file"]');
        expect(element).not.toBeNull();
        return element!;
      });
      fireEvent.change(input, {
        target: { files: [new File(['image'], name, { type: 'image/png' })] },
      });
    };
    await replace('first.png');
    await waitFor(() => expect(patchSlotItemValue).toHaveBeenNthCalledWith(
      1, 'image-session', 'images', 0,
      { path: '/var/lib/lazymind/uploads/first.png' },
      'image', 'checkpoint', 1, undefined,
    ));

    rerender(
      <SlotRenderer
        slot={{
          ...slot,
          revision: 2,
          draft_version: 1,
          change_source: 'human',
          artifact_value: { url: 'https://example.test/first.png' },
        }}
        expectedType='image'
        cardMode
        sessionId='image-session'
        slotId='images'
      />,
    );
    await replace('second.png');
    await waitFor(() => expect(patchSlotItemValue).toHaveBeenNthCalledWith(
      2, 'image-session', 'images', 0,
      { path: '/var/lib/lazymind/uploads/second.png' },
      'image', 'checkpoint', 2, 1,
    ));
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

  it('uploads an image version against the selected draft baseline', async () => {
    const getSlotVersions = vi.fn().mockResolvedValue([{
      revision: 2,
      draft_version: 1,
      change_source: 'human',
      created_at: '2026-09-10T00:00:00Z',
      selected: true,
      content_snapshot: { url: 'https://example.test/current.png' },
    }]);
    const patchSlotItemValue = vi.fn().mockResolvedValue(3);
    useWorkflowStore.setState({ getSlotVersions, patchSlotItemValue });
    chunkUpload.uploadFileInChunks.mockReset();
    chunkUpload.uploadFileInChunks.mockResolvedValue('/var/lib/lazymind/uploads/replacement.png');
    const { container } = render(
      <SlotVersionPopover
        sessionId='image-session'
        slotId='images'
        listIndex={0}
        revisionCount={1}
        currentRevision={2}
        currentValue={{ url: 'https://example.test/current.png' }}
        currentChangeSource='human'
        contentType='image'
      />,
    );

    fireEvent.click(container.querySelector<HTMLButtonElement>('.workflow-slot__version-btn')!);
    const input = await waitFor(() => {
      const element = document.querySelector<HTMLInputElement>(
        '.workflow-slot__version-popover input[type="file"]',
      );
      expect(element).not.toBeNull();
      return element!;
    });
    fireEvent.change(input, {
      target: { files: [new File(['image'], 'replacement.png', { type: 'image/png' })] },
    });

    await waitFor(() => expect(patchSlotItemValue).toHaveBeenCalledWith(
      'image-session', 'images', 0,
      { path: '/var/lib/lazymind/uploads/replacement.png' },
      'image', 'checkpoint', 2, 1,
    ));
  });

  it('keeps the local draft when version-popover confirmation conflicts', async () => {
    const flush = vi.spyOn(draftStore, 'flushDraft').mockResolvedValue(false);
    const onDiscardDraft = vi.fn();
    useWorkflowStore.setState({
      getSlotVersions: vi.fn().mockResolvedValue([{
        revision: 1,
        draft_version: 4,
        change_source: 'human',
        created_at: '2026-09-10T00:00:00Z',
        selected: true,
        content_snapshot: '# Stored',
      }]),
    });
    const { container } = render(
      <SlotVersionPopover
        sessionId='session'
        slotId='draft_document'
        listIndex={-1}
        draftListIndex={0}
        revisionCount={1}
        currentRevision={1}
        currentValue='# Stored'
        currentChangeSource='human'
        draftText='# Local draft'
        onDiscardDraft={onDiscardDraft}
      />,
    );

    fireEvent.click(container.querySelector<HTMLButtonElement>('.workflow-slot__version-btn')!);
    const confirm = await waitFor(() => {
      const button = document.querySelector<HTMLButtonElement>('.workflow-slot__version-flush-btn');
      expect(button).not.toBeNull();
      return button!;
    });
    fireEvent.click(confirm);
    await waitFor(() => expect(flush).toHaveBeenCalled());
    expect(onDiscardDraft).not.toHaveBeenCalled();
    expect(document.querySelector('.workflow-slot__version-popover')).not.toBeNull();
    flush.mockRestore();
  });
});

describe('Markdown file write-back baseline lifecycle', () => {
  beforeEach(() => {
    modalConfirm.mockReset();
    workflowApi.writeBackWriterDocument.mockReset();
    workflowApi.writeBackWriterDocument
      .mockResolvedValueOnce({
        data: { code: 0, data: {
          status: 'synced', revision: 2, draft_version: 1,
          provider_synced: true, artifact_saved: true,
          patch_result: { success: true }, representation: 'markdown', document: '# Synced 1',
        } },
      })
      .mockResolvedValueOnce({
        data: { code: 0, data: {
          status: 'synced', revision: 3, draft_version: 1,
          provider_synced: true, artifact_saved: true,
          patch_result: { success: true }, representation: 'markdown', document: '# Synced 2',
        } },
      });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      text: async () => '# Draft',
    }));
  });

  it('uses the returned write-back baseline for the next action on the same mount', async () => {
    let writeBackAction: SlotFooterAction | undefined;
    const registerFooterAction = (_key: string, action: SlotFooterAction | null) => {
      if (action?.icon === 'write-back') writeBackAction = action;
      return () => {};
    };
    const slot: SlotRevision = {
      slot_id: 'draft_document', revision: 1, draft_version: 4, selected: true,
      slot: 'draft_document', created_at: '2026-09-10T00:00:00Z', content_type: 'file',
      artifact_value: { filename: 'draft.md', url: 'https://example.test/draft.md' },
      change_source: 'human', write_back_ready: true, write_back_state: 'initial_delivery',
      provider: 'notion',
    };
    render(
      <SlotEditingContext.Provider value={{
        setEditing: vi.fn(), registerFlush: () => () => {}, registerFooterAction,
      }}>
        <SlotRenderer
          slot={slot}
          expectedType='file'
          sessionId='writer-session'
          slotId='draft_document'
        />
      </SlotEditingContext.Provider>,
    );

    await waitFor(() => expect(writeBackAction).toBeDefined());
    writeBackAction!.onClick();
    await waitFor(() => expect(modalConfirm).toHaveBeenCalledTimes(1));
    act(() => { modalConfirm.mock.calls[0][0].onOk(); });
    await waitFor(() => expect(workflowApi.writeBackWriterDocument).toHaveBeenNthCalledWith(
      1, 'writer-session', 1, 4, undefined, undefined, 'draft_document', 'notion', undefined,
      { silentError: true },
    ));

    writeBackAction!.onClick();
    await waitFor(() => expect(modalConfirm).toHaveBeenCalledTimes(2));
    act(() => { modalConfirm.mock.calls[1][0].onOk(); });
    await waitFor(() => expect(workflowApi.writeBackWriterDocument).toHaveBeenNthCalledWith(
      2, 'writer-session', 2, 1, undefined, undefined, 'draft_document', 'notion', undefined,
      { silentError: true },
    ));
  });
});
