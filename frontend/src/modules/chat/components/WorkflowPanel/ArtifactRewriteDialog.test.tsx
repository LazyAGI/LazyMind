import { act, fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react';
import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const workflowApi = vi.hoisted(() => ({ patchSlotItem: vi.fn(), previewRewriteSelection: vi.fn(), executeArtifactAction: vi.fn() }));

vi.mock('@/modules/chat/utils/request', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/modules/chat/utils/request')>(),
  WorkflowSessionApi: () => workflowApi,
}));

import {
  ArtifactRewriteDialog,
  ArtifactRewriteInlineDiff,
  type ArtifactRewriteSelection,
} from './ArtifactRewriteDialog';

const previewApi = workflowApi.previewRewriteSelection;
const executeApi = workflowApi.executeArtifactAction;

vi.mock('react-i18next', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-i18next')>();
  const translate = (key: string) => key;
  return {
    ...actual,
    useTranslation: () => ({ t: translate }),
  };
});

const selection: ArtifactRewriteSelection = {
  type: 'markdown',
  selected_text: 'Selected text',
  selectedText: 'Selected text',
  anchor: { top: 120, left: 240, placement: 'above' },
};

const readyPreview = {
  status: 'ready', action: 'rewrite_selection', base_revision: 1, representation: 'markdown',
  target: { type: 'block', block_type: 'paragraph' },
  preview: { old_text: 'Selected text', new_text: 'Clear text' },
  patch: { type: 'string_replace_set', payload: {} },
  artifact: { content_type: 'text/markdown', value: 'Clear text' },
} as const;

function renderDialog(requestPreview = vi.fn()) {
  render(
    <ArtifactRewriteDialog
      open
      sessionId='session-1'
      slotId='draft_document'
      listIndex={0}
      baseRevision={1}
      selection={selection}
      onClose={vi.fn()}
      onApplied={vi.fn()}
      requestPreview={requestPreview}
    />,
  );
  return requestPreview;
}

describe('ArtifactRewriteDialog', () => {
  beforeEach(() => {
    workflowApi.patchSlotItem.mockReset();
    previewApi.mockReset();
    executeApi.mockReset();
  });

  it.each(['markdown', 'ir'] as const)('shows non-blocking progress in the %s document until preview is ready', async type => {
    const host = document.createElement('div'); host.className = 'workflow-slot__artifact-body';
    host.innerHTML = '<div contenteditable="true" tabindex="0"><p>Selected text</p><p>Other content</p></div>';
    document.body.append(host);
    const editor = host.firstElementChild as HTMLElement;
    editor.focus();
    let finish!: (value: typeof readyPreview) => void;
    const request = vi.fn(() => new Promise<typeof readyPreview>(resolve => { finish = resolve; }));
    const onReady = vi.fn();
    const picked: ArtifactRewriteSelection = type === 'markdown'
      ? { ...selection, paragraph: editor.querySelector('p')! }
      : { type: 'ir', node_id: 'p1', selectedText: 'Selected text' };
    function Form() {
      const [open, setOpen] = useState(true);
      return <ArtifactRewriteDialog open={open} sessionId='session' slotId='draft_document' listIndex={0} baseRevision={1}
        selection={picked} onClose={() => setOpen(false)} onApplied={vi.fn()} onPreviewReady={onReady} requestPreview={request} />;
    }
    try {
      render(<Form />);
      fireEvent.change(screen.getByRole('textbox', { name: 'chat.artifactRewrite.instruction' }), { target: { value: 'Make clearer' } });
      fireEvent.click(screen.getByRole('button', { name: 'chat.artifactRewrite.preview' }));
      expect(screen.getByRole('status')).toHaveTextContent('chat.artifactRewrite.previewing');
      expect(screen.getByRole('status')).toHaveTextContent('chat.writerLocal.loadingCount');
      expect(screen.getByRole('status').parentElement).toBe(host);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(screen.queryByRole('textbox', { name: 'chat.artifactRewrite.instruction' })).not.toBeInTheDocument();
      fireEvent.mouseDown(editor); editor.focus();
      editor.querySelectorAll('p')[1].textContent = 'Edited while waiting';
      fireEvent.input(editor);
      expect(editor).toHaveAttribute('contenteditable', 'true');
      expect(editor).toHaveFocus();
      expect(request).toHaveBeenCalledTimes(1);
      await act(async () => finish(readyPreview));
      expect(screen.queryByRole('status')).not.toBeInTheDocument();
      expect(onReady).toHaveBeenCalledWith(readyPreview);
      expect(editor).toHaveTextContent('Edited while waiting');
      expect(editor).toHaveFocus();
    } finally { cleanup(); host.remove(); }
  });

  it('replaces loading with a retryable error and keeps the polishing instruction', async () => {
    let fail!: (reason: Error) => void;
    const request = vi.fn(() => new Promise<never>((_, reject) => { fail = reject; }));
    renderDialog(request);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Keep my instruction' } });
    fireEvent.click(screen.getByRole('button', { name: 'chat.artifactRewrite.preview' }));
    expect(screen.getByRole('status')).toBeInTheDocument();
    await act(async () => fail(new Error('request failed')));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('chat.artifactRewrite.errors.previewFailed');
    expect(screen.getByRole('textbox')).toHaveValue('Keep my instruction');
    expect(screen.getByRole('button', { name: 'chat.artifactRewrite.preview' })).toBeEnabled();
  });

  it('removes progress on unmount and ignores a late preview response', async () => {
    let finish!: (value: typeof readyPreview) => void;
    const request = vi.fn(() => new Promise<typeof readyPreview>(resolve => { finish = resolve; }));
    const onReady = vi.fn(), onClose = vi.fn();
    const view = render(<ArtifactRewriteDialog open sessionId='session' slotId='draft_document' listIndex={0} baseRevision={1}
      selection={selection} onClose={onClose} onApplied={vi.fn()} onPreviewReady={onReady} requestPreview={request} />);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Make clearer' } });
    fireEvent.click(screen.getByRole('button', { name: 'chat.artifactRewrite.preview' }));
    expect(screen.getByRole('status')).toBeInTheDocument();
    view.unmount();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    await act(async () => finish(readyPreview));
    expect(onReady).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it.each([
    ['ir', 6], ['markdown', 6], ['ir', undefined], ['markdown', undefined],
  ] as const)('accepts the single paragraph with the preview commit token (%s, draft %s)', async (representation, draftVersion) => {
    const target = document.createElement('p');
    target.textContent = 'Original';
    const layer = document.createElement('div');
    document.body.append(target, layer);
    executeApi.mockResolvedValue({ data: { code: 0, data: { status: 'applied', revision: 5, draft_version: 1 } } });
    const onApplied = vi.fn();
    try {
      render(<ArtifactRewriteInlineDiff target={target} layer={layer} sessionId='session'
        slotId='draft_document' listIndex={-1} onApplied={onApplied} onReject={vi.fn()}
        preview={{ status: 'ready', action: 'rewrite_selection', base_revision: 4,
          base_draft_version: draftVersion, representation, target: { type: 'block', block_type: 'paragraph' },
          preview: { old_text: 'Original', new_text: 'Polished' },
          patch: { type: 'string_replace_set', payload: {} },
          artifact: { content_type: 'text', value: 'Polished' }, commit: { token: 'preview-token' } }} />);
      fireEvent.click(screen.getByRole('button', { name: 'chat.artifactRewrite.apply' }));
      await waitFor(() => expect(onApplied).toHaveBeenCalledWith(5, 1));
      expect(executeApi).toHaveBeenCalledWith('session', 'draft_document', -1, {
        action: 'rewrite_selection', base_revision: 4,
        ...(draftVersion !== undefined ? { base_draft_version: draftVersion } : {}),
        input: { commit_token: 'preview-token' },
      });
    } finally {
      cleanup();
      target.remove();
      layer.remove();
    }
  });
  it.each(['ir', 'markdown'] as const)('sends %s type once with one selected paragraph', async (type) => {
    const selected = type === 'ir'
      ? { type, node_id: 'p1', selectedText: 'Selected text' }
      : selection;
    previewApi.mockResolvedValue({ data: { data: {
      status: 'ready', action: 'rewrite_selection', base_revision: 1, representation: type,
      target: { type: 'block', block_type: 'paragraph' },
      preview: { old_text: 'Full paragraph', new_text: 'Polished paragraph' },
      patch: { type: type === 'ir' ? 'writer_ir_patch' : 'string_replace_set', payload: {} },
      artifact: { content_type: 'text', value: 'Polished paragraph' },
    } } });
    const onPreviewReady = vi.fn();
    render(<ArtifactRewriteDialog open sessionId='session' slotId='draft_document' listIndex={-1}
      baseRevision={1} selection={selected} onClose={vi.fn()} onApplied={vi.fn()}
      onPreviewReady={onPreviewReady} />);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Polish' } });
    fireEvent.click(screen.getByRole('button', { name: 'chat.artifactRewrite.preview' }));
    await waitFor(() => expect(onPreviewReady).toHaveBeenCalled());
    expect(previewApi.mock.lastCall?.[3].input).toEqual({
      type, instruction: 'Polish', selection_ranges: [type === 'ir'
        ? { node_id: 'p1', selected_text: 'Selected text' } : { selected_text: 'Selected text' }],
    });
  });

  it('explains ambiguous selections inside a numeric API error envelope', async () => {
    renderDialog(vi.fn().mockRejectedValue({
      response: { data: { code: 2000107, message: 'Conflict', data: {
        code: 'SELECTION_AMBIGUOUS', match_count: 2,
      } } },
    }));
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Make it clearer' } });
    fireEvent.click(screen.getByRole('button', { name: 'chat.artifactRewrite.preview' }));
    expect(await screen.findByText('chat.artifactRewrite.errors.ambiguous')).toBeVisible();
    expect(screen.queryByText('chat.artifactRewrite.errors.previewFailed')).not.toBeInTheDocument();
  });

  it('does not submit an empty or whitespace-only instruction', () => {
    const requestPreview = renderDialog();
    const input = screen.getByRole('textbox');
    const submit = screen.getByRole('button', { name: 'chat.artifactRewrite.preview' });

    expect(submit).toBeDisabled();
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(requestPreview).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: '   ' } });
    expect(submit).toBeDisabled();
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(requestPreview).not.toHaveBeenCalled();
  });

  it.each(['concise', 'fluent', 'formal'])('prepares the full %s instruction without submitting and clears its selected state after a custom edit', key => {
    const requestPreview = renderDialog();
    const preset = screen.getByRole('button', { name: `chat.writerLocal.${key}` });
    fireEvent.click(preset);
    expect(screen.getByRole('textbox')).toHaveValue(`chat.writerLocal.${key}Instruction`);
    expect(preset).toHaveAttribute('aria-pressed', 'true');
    expect(requestPreview).not.toHaveBeenCalled();
    expect(fireEvent.keyDown(preset, { key: 'Enter' })).toBe(true);
    expect(requestPreview).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'A custom instruction' } });
    expect(preset).toHaveAttribute('aria-pressed', 'false');
  });

  it('allows a line break and IME confirmation without sending, then submits the complete instruction', async () => {
    const requestPreview = renderDialog(vi.fn().mockResolvedValue(readyPreview));
    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: '保留原意' } });
    expect(fireEvent.keyDown(input, { key: 'Enter', shiftKey: true })).toBe(true);
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true, keyCode: 229 });
    expect(requestPreview).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: '保留原意\n语言更自然' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(requestPreview).toHaveBeenCalledWith('保留原意\n语言更自然', selection));
  });

  it('offers an explicit close action without generating a preview', () => {
    const onClose = vi.fn(), requestPreview = vi.fn();
    render(<ArtifactRewriteDialog open sessionId='session' slotId='draft_document' listIndex={0} baseRevision={1}
      selection={selection} onClose={onClose} onApplied={vi.fn()} requestPreview={requestPreview} />);
    fireEvent.click(screen.getByRole('button', { name: 'chat.artifactRewrite.close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(requestPreview).not.toHaveBeenCalled();
  });

  it('submits a trimmed non-empty instruction', async () => {
    const requestPreview = renderDialog(vi.fn().mockResolvedValue({
      status: 'ready',
      action: 'rewrite_selection',
      base_revision: 1,
      representation: 'markdown',
      target: { type: 'block', block_type: 'paragraph' },
      preview: { old_text: 'Selected text', new_text: 'Rewritten text' },
      patch: { type: 'string_replace_set', payload: {} },
      artifact: { content_type: 'text/markdown', value: 'Rewritten text' },
    }));
    const input = screen.getByRole('textbox');
    const submit = screen.getByRole('button', { name: 'chat.artifactRewrite.preview' });

    fireEvent.change(input, { target: { value: '  Make it clearer  ' } });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);

    await waitFor(() => {
      expect(requestPreview).toHaveBeenCalledWith('Make it clearer', selection);
    });
  });

  it('returns both revision baselines after applying a preview', async () => {
    workflowApi.patchSlotItem.mockResolvedValue({
      data: {
        code: 0,
        data: { type: 'slot_item_patched', revision: 3, draft_version: 7 },
      },
    });
    const target = document.createElement('p');
    target.textContent = 'Selected text';
    const layer = document.createElement('div');
    document.body.append(target, layer);
    const onApplied = vi.fn();

    render(
      <ArtifactRewriteInlineDiff
        target={target}
        layer={layer}
        sessionId='session-1'
        slotId='draft_document'
        listIndex={-1}
        preview={{
          status: 'ready',
          action: 'rewrite_selection',
          base_revision: 3,
          base_draft_version: 6,
          representation: 'markdown',
          target: { type: 'block', block_type: 'paragraph' },
          preview: { old_text: 'Selected text', new_text: 'Rewritten text' },
          patch: { type: 'string_replace_set', payload: {} },
          artifact: { content_type: 'text/markdown', value: 'Rewritten text' },
        }}
        onApplied={onApplied}
        onReject={vi.fn()}
      />,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'chat.artifactRewrite.apply' }));
    await waitFor(() => expect(onApplied).toHaveBeenCalledWith(3, 7));
  });
});
