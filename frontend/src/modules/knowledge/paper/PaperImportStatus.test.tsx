import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import PaperImportStatus from './PaperImportStatus';
import type { AcademicImportQueueItem } from '../api/academicReferences';

afterEach(cleanup);
const item = (id: string, work = 'lora', extra: Partial<AcademicImportQueueItem> = {}): AcademicImportQueueItem => ({
  item_id: id, batch_id: id, work_id: work, title: work, target_dataset_id: 'papers', source_provider: 'arxiv', source_version: 'v2',
  status: 'failed', batch_status: 'failed', stage: 'acquire', created_at: `2026-10-23T10:0${id}:00Z`, updated_at: `2026-10-23T10:0${id}:00Z`,
  error_message: 'arXiv 元数据服务返回 HTTP 429，查询失败', ...extra,
});

describe('paper import history', () => {
  it('collapses historical failures and reports only unresolved paper versions', () => {
    render(<PaperImportStatus canWrite onRetry={vi.fn()} items={[item('1'), item('2'), item('3'), item('4', 'qlora'), item('5', 'qlora', { status: 'succeeded', current_document: { document_id: 'pdf', processing_status: 'ready' } })]} />);
    expect(screen.getByText('论文导入：1 篇待处理 · 0 篇进行中')).toBeInTheDocument();
    expect(screen.queryByText('lora')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('论文导入：1 篇待处理 · 0 篇进行中'));
    expect(screen.getAllByText('lora')).toHaveLength(1);
    expect(screen.getByText('已入库')).toBeInTheDocument();
    expect(screen.getByText('历史记录（3 次）')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /重试本批次/ })).toHaveLength(1);
    expect(screen.queryByText('arXiv 元数据服务返回 HTTP 429，查询失败')).not.toBeInTheDocument();
  });
  it('retries a batch once and leaves its successful items alone', async () => {
    let finish!: () => void;
    const retry = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    render(<PaperImportStatus canWrite onRetry={retry} items={[item('1'), item('2', 'other', { batch_id: '1' }), item('3', 'ready', { batch_id: '1', status: 'succeeded' })]} />);
    fireEvent.click(screen.getByText('论文导入：2 篇待处理 · 0 篇进行中'));
    const button = screen.getByRole('button', { name: '重试本批次失败项（2 篇）' });
    fireEvent.click(button); fireEvent.click(button);
    await waitFor(() => expect(retry).toHaveBeenCalledTimes(1));
    expect(retry).toHaveBeenCalledWith('1');
    expect(button).toBeDisabled();
    await act(async () => { finish(); });
  });
  it('keeps processing failures visible and does not offer retries to a read-only user', () => {
    render(<PaperImportStatus canWrite={false} onRetry={vi.fn()} items={[item('1', 'lora', { current_document: { document_id: 'pdf', processing_status: 'failed', processing_error: '解析失败' } })]} />);
    fireEvent.click(screen.getByText('论文导入：1 篇待处理 · 0 篇进行中'));
    expect(screen.getByText('PDF 已保存，处理失败')).toBeInTheDocument();
    expect(screen.getByText('解析失败')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '查看文档' })).toHaveAttribute('href', '/lib/knowledge/knowledge/papers/pdf');
    expect(screen.queryByRole('button', { name: /重试本批次/ })).not.toBeInTheDocument();
  });
  it('treats a submitted retry as active before the worker changes item status', () => {
    render(<PaperImportStatus canWrite onRetry={vi.fn()} items={[item('1', 'lora', { batch_status: 'pending' })]} />);
    fireEvent.click(screen.getByText('论文导入：0 篇待处理 · 1 篇进行中'));
    expect(screen.getByText('导入中')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /重试本批次/ })).not.toBeInTheDocument();
  });
});
