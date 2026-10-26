import { libraryFixture, paperFixture } from "./__tests__/fixtures";
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';
import PaperDetailDrawer from './PaperDetailDrawer';
const fixture=()=>libraryFixture({papers:[paperFixture({metadata:{year:2026}})]});
afterEach(cleanup);
describe('durable paper notes', () => {
    it('retains note text through a delayed failure and clears it only after a successful retry', async () => {
        const data = fixture();
        let reject!: (error: Error) => void;
        let acknowledge!: () => void;
        let saved: {id:string,content:string}|undefined;
        const onAddNote = vi.fn<(id:string,content:string) => Promise<void>>()
            .mockImplementationOnce(() => new Promise((_, fail) => { reject = fail; }))
            .mockImplementationOnce((id,content) => new Promise((resolve) => { acknowledge = () => { saved = {id,content}; resolve(); }; }));
        render(<ConfigProvider theme={{ token: { motion: false } }}><PaperDetailDrawer datasetId="library" paper={data.papers[0]} data={data} onClose={() => { }} onAddNote={onAddNote}/></ConfigProvider>);
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
        expect(saved?.content).toBe('Do not lose this draft');
        expect(onAddNote.mock.calls[0][0]).toBe(onAddNote.mock.calls[1][0]);
    });
    it('retries failed metadata recognition separately and prevents duplicate submission', async () => {
        const data = fixture();
        data.papers[0].parse = 'failed';
        data.papers[0].processingError = 'invalid literal for int()';
        let finish!: () => void;
        const retry = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
        render(<ConfigProvider theme={{ token: { motion: false } }}><PaperDetailDrawer datasetId="library" paper={data.papers[0]} data={data} academic={{ state: { document_id: 'pdf', kind: 'unknown', status: 'failed', paper_id: '', manual: false, error: 'Model unavailable' }, is_important: false }} onClose={() => { }} onAddNote={vi.fn()} onRetryIdentification={retry} onSave={vi.fn().mockResolvedValue(true)}/></ConfigProvider>);
        expect(screen.getByRole('status', { name: '论文信息自动识别' })).toHaveTextContent('识别失败');
        expect(screen.getByRole('status', { name: '正文解析状态' })).toHaveTextContent('正文解析失败');
        expect(screen.getByRole('button', { name: '编 辑' })).toBeEnabled();
        expect(screen.queryByText('Model unavailable')).not.toBeInTheDocument();
        expect(screen.queryByText('invalid literal for int()')).not.toBeInTheDocument();
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '重试自动识别' }));
        expect(screen.getByRole('button', { name: '重试自动识别' })).toBeDisabled();
        expect(retry).toHaveBeenCalledOnce();
        await act(async () => finish());
        await waitFor(() => expect(screen.getByRole('button', { name: '重试自动识别' })).toBeEnabled());
        expect(data.papers[0].metadata.title).toBe('A study');
    });
});
