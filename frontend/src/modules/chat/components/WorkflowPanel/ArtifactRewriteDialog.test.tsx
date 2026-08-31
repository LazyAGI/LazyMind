import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ArtifactRewriteDialog, type ArtifactRewriteSelection } from './ArtifactRewriteDialog';

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
});
