import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { migratePaperLibrary, type PaperLibraryDataV3 } from './libraryModel';
import PaperDetailDrawer from './PaperDetailDrawer';

const fixture = () => migratePaperLibrary({ folders: [], papers: [{ id: 'paper', title: 'A study', authors: 'A. Author', year: 2026, venue: 'Journal', folderId: null, status: 'unread', important: false, tags: [], source: 'local', parse: 'ready' }] });

afterEach(cleanup);

describe('durable paper notes', () => {
  it('retains note text through a delayed failure and clears it only after a successful retry', async () => {
    const data = fixture();
    let reject!: (error: Error) => void;
    let acknowledge!: () => void;
    let saved: PaperLibraryDataV3 | undefined;
    const onChange = vi.fn<(next: PaperLibraryDataV3) => Promise<void>>()
      .mockImplementationOnce(() => new Promise((_, fail) => { reject = fail; }))
      .mockImplementationOnce((next) => new Promise((resolve) => { acknowledge = () => { saved = next; resolve(); }; }));
    render(<ConfigProvider theme={{ token: { motion: false } }}><PaperDetailDrawer paper={data.papers[0]} data={data} onClose={() => {}} onChange={onChange} /></ConfigProvider>);
    fireEvent.click(screen.getByRole('tab', { name: '笔记 0' }));
    const input = screen.getByPlaceholderText('写下这篇文献的阅读笔记');
    fireEvent.change(input, { target: { value: 'Do not lose this draft' } });
    fireEvent.click(screen.getByRole('button', { name: '添加笔记' }));
    expect(input).toHaveValue('Do not lose this draft');
    expect(screen.getByRole('button', { name: /添加笔记/ })).toBeDisabled();
    await act(async () => reject(new Error('后端保存失败')));
    expect(screen.getByRole('alert')).toHaveTextContent('后端保存失败');
    expect(input).toHaveValue('Do not lose this draft');
    expect(data.notes).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: '添加笔记' }));
    expect(input).toHaveValue('Do not lose this draft');
    await act(async () => acknowledge());
    expect(input).toHaveValue('');
    expect(saved?.notes).toHaveLength(1);
    expect(saved?.notes[0].content).toBe('Do not lose this draft');
    expect(saved?.papers[0].noteIds).toEqual([saved?.notes[0].id]);
    expect(saved?.papers[0].metadata).toEqual(data.papers[0].metadata);
  });
  it('retries failed metadata recognition separately and prevents duplicate submission', async () => {
    const data = fixture();
    let finish!: () => void;
    const retry = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    render(<ConfigProvider theme={{ token: { motion: false } }}><PaperDetailDrawer paper={data.papers[0]} data={data} academic={{ state: { document_id: 'pdf', kind: 'unknown', status: 'failed', paper_id: '', manual: false, error: 'Model unavailable' }, is_important: false }} onClose={() => {}} onChange={vi.fn()} onRetryIdentification={retry} /></ConfigProvider>);
    fireEvent.click(screen.getByRole('button', { name: '重试自动识别' }));
    expect(screen.getByRole('button', { name: '重试自动识别' })).toBeDisabled();
    expect(retry).toHaveBeenCalledOnce();
    await act(async () => finish());
    await waitFor(() => expect(screen.getByRole('button', { name: '重试自动识别' })).toBeEnabled());
    expect(data.papers[0].title).toBe('A study');
  });

});
