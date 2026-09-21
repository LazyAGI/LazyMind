import { act, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SlotEditingContext, type SlotFooterAction } from './slotEditingContext';
import { useDocumentCopy } from './useDocumentCopy';

const mocks = vi.hoisted(() => ({
  convert: vi.fn(), success: vi.fn(), error: vi.fn(), confirm: vi.fn(),
}));
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => ({ convertDocument: mocks.convert }) }));
vi.mock('antd', () => ({
  message: { success: mocks.success, error: mocks.error },
  Modal: { confirm: mocks.confirm }, Input: { TextArea: () => <textarea /> },
}));

function Harness({ revision = 3, draftVersion, sourceKey }: { revision?: number; draftVersion?: number; sourceKey?: string }) {
  useDocumentCopy({
    enabled: true, editingKey: 'editor', sessionId: 'session', slotId: 'custom_article',
    revision, draftVersion, document: '# Stored', sourceKey,
  });
  return null;
}

describe('document copy', () => {
  let action: SlotFooterAction;
  let writeText: ReturnType<typeof vi.fn>;
  const flush = vi.fn();
  const register = (_key: string, value: SlotFooterAction | null) => {
    if (value) action = value;
    return () => {};
  };
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <SlotEditingContext.Provider value={{
      setEditing: vi.fn(), registerFlush: flush, registerFooterAction: register,
      getSnapshot: () => '# Unsaved',
    }}>{children}</SlotEditingContext.Provider>
  );

  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    mocks.convert.mockImplementation(async (_session, _slot, _index, _revision, format) => ({
      data: { code: 0, data: { format, provider: '', content: 'converted content' } },
    }));
  });

  it('copies the latest unsaved snapshot without flushing or writing back', async () => {
    const view = render(<Harness />, { wrapper });
    view.rerender(<Harness revision={4} />);
    act(() => action.onClick());
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('converted content'));
    expect(mocks.convert).toHaveBeenCalledWith('session', 'custom_article', -1, 4, 'markdown', '# Unsaved', undefined);
    expect(flush).not.toHaveBeenCalled();
    expect(action.flushBeforeAction).toBeUndefined();
    expect(action.label).toContain('chat.writerCopy.success');
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it.each(['markdown', 'latex', 'text'])('copies %s with the latest mutable draft baseline', async (format) => {
    const view = render(<Harness revision={4} draftVersion={7} />, { wrapper });
    view.rerender(<Harness revision={4} draftVersion={8} />);
    act(() => action.menu?.find(item => item.key === format)?.onClick());
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('converted content'));
    expect(mocks.convert).toHaveBeenLastCalledWith('session', 'custom_article', -1, 4, format, '# Unsaved', 8);
    expect(flush).not.toHaveBeenCalled();
  });

  it('identifies shared read-only sources without merging independent editors', () => {
    const view = render(<Harness sourceKey='/exports/paper.md' />, { wrapper });
    expect(action.dedupKey).toBe(JSON.stringify(['copy', 'session', '/exports/paper.md']));
    view.rerender(<Harness />);
    expect(action.dedupKey).toBeUndefined();
  });

  it('remembers the selected format and supports one-click reuse', async () => {
    render(<Harness />, { wrapper });

    expect(action.label).toBe('chat.writerCopy.copyContent');
    expect(action.selectedMenuKey).toBe('markdown');
    act(() => action.menu?.find((item) => item.key === 'latex')?.onClick());
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    expect(localStorage.getItem('writer-copy-format')).toBe('latex');
    expect(action.selectedMenuKey).toBe('latex');
    expect(action.label).toBe('chat.writerCopy.success');
    act(() => action.onClick());
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(2));
    expect(mocks.convert.mock.calls[1][4]).toBe('latex');
  });

  it('restores the last format when the document is opened again', () => {
    localStorage.setItem('writer-copy-format', 'text');
    render(<Harness />, { wrapper });
    expect(action.selectedMenuKey).toBe('text');
  });

  it('retains the converted text for retry if clipboard permission is denied', async () => {
    writeText.mockRejectedValueOnce(new Error('denied'));
    render(<Harness />, { wrapper });
    act(() => action.onClick());
    await waitFor(() => expect(mocks.confirm).toHaveBeenCalled());
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.confirm.mock.calls[0][0].content.props.value).toBe('converted content');
    await act(async () => { await mocks.confirm.mock.calls[0][0].onOk(); });
    expect(mocks.convert).toHaveBeenCalledTimes(1);
    expect(action.label).toContain('chat.writerCopy.success');
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it('does not touch the clipboard when conversion fails', async () => {
    mocks.convert.mockRejectedValueOnce(new Error('conflict'));
    render(<Harness />, { wrapper });
    act(() => action.onClick());
    await waitFor(() => expect(mocks.error).toHaveBeenCalled());
    expect(writeText).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });
});
