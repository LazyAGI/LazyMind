import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState, type ComponentProps } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import PaperImportStatus from './PaperImportStatus';
import { emptyImportBatchPage, getAcademicImportBatch, listAcademicImportBatches, type ImportOperation, type ImportOperationItem } from '../api/academicReferences';
vi.mock('../api/academicReferences', async original => ({ ...await original<typeof import('../api/academicReferences')>(), getAcademicImportBatch: vi.fn(), listAcademicImportBatches: vi.fn() }));
afterEach(cleanup);
beforeEach(() => vi.clearAllMocks());
const operation = (id: string, extra: Partial<ImportOperation> = {}): ImportOperation => ({
  id, entry_type: 'paper_expansion', target_dataset_id: 'papers', source: { title: '研究方向' }, status: 'succeeded', created_at: '2026-10-23T02:00:00Z',
  summary: { total: 2, ready: 1, metadata_only: 0, skipped: 1, active: 0, failed: 0, canceled: 0, deleted: 0, removed: 0, resolved_later: 0, retryable: 0, needs_action: 0 }, ...extra,
});
const paper = (id: string, extra: Partial<ImportOperationItem> = {}): ImportOperationItem => ({
  item_id: id, batch_id: 'batch', work_id: id, title: `Paper ${id}`, target_dataset_id: 'papers', source_provider: 'arxiv', source_version: 'v2',
  status: 'skipped', stage: 'done', result_kind: 'already_present', current_state: 'skipped', document_deleted: false, resolved_later: false, can_retry: false,
  current_document: { document_id: id, processing_status: 'ready' }, created_at: '2026-10-23T02:00:00Z', updated_at: '2026-10-23T02:00:00Z', ...extra,
});
function Notice(props: Omit<ComponentProps<typeof PaperImportStatus>, 'open' | 'onOpenChange' | 'datasetId' | 'onOpenPaper'> & { onOpenPaper?: (id: string) => void }) {
  const [open, setOpen] = useState(false);
  return <PaperImportStatus {...props} datasetId="papers" onOpenPaper={props.onOpenPaper || vi.fn()} open={open} onOpenChange={setOpen} />;
}
const openNotice = () => fireEvent.click(screen.getByRole('button', { name: /^通知：/ }));
describe('paper import operations', () => {
  it('keeps an empty notification entry and closes its panel', () => {
    render(<Notice canWrite onRetry={vi.fn()} page={emptyImportBatchPage()} />); openNotice();
    expect(screen.getByText('暂无论文导入记录')).toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '关闭通知' }));
    expect(screen.getByRole('button', { name: /^通知：/ })).toHaveAttribute('aria-expanded', 'false');
  });
  it('shows one operation and links paper titles without misreporting skipped results', async () => {
    const batch = operation('batch');
    vi.mocked(getAcademicImportBatch).mockResolvedValue({ operation: batch, items: [paper('flash'), paper('qlora', { status: 'succeeded', result_kind: 'imported', current_state: 'ready' })], next_cursor: '' });
    render(<Notice canWrite onRetry={vi.fn()} page={{ ...emptyImportBatchPage(), items: [batch] }} />); openNotice();
    expect(screen.getByText('扩展导入 · 2 篇')).toBeInTheDocument(); expect(screen.queryByText('Paper flash')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /^展开导入记录/ }));
    expect(await screen.findByRole('link', { name: 'Paper flash' })).toHaveAttribute('href', '/lib/knowledge/knowledge/papers/flash');
    expect(screen.getByText('本次跳过，复用已有 PDF')).toBeInTheDocument();
    expect(screen.queryByText('查看文档')).not.toBeInTheDocument(); expect(screen.queryByText(/历史记录/)).not.toBeInTheDocument();
  });
  it('shows removed membership while keeping the original PDF readable', async () => {
    const batch = operation('removed', { status: 'removed', summary: { ...operation('removed').summary, ready: 0, skipped: 0, removed: 2 } });
    vi.mocked(getAcademicImportBatch).mockResolvedValue({ operation: batch, items: [paper('pdf', { status: 'succeeded', result_kind: 'imported', current_state: 'removed' }), paper('metadata', { current_document: undefined, current_state: 'removed' })], next_cursor: '' });
    render(<Notice canWrite onRetry={vi.fn()} page={{ ...emptyImportBatchPage(), items: [batch] }} />); openNotice();
    expect(screen.getByText('已移出论文库')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /^展开导入记录/ }));
    expect(await screen.findByRole('link', { name: 'Paper pdf' })).toHaveAttribute('href', '/lib/knowledge/knowledge/papers/pdf');
    expect(screen.getAllByText('已移出论文库')).toHaveLength(3);
    expect(screen.queryByRole('button', { name: 'Paper metadata' })).not.toBeInTheDocument();
    expect(screen.queryByText('已入库')).not.toBeInTheDocument();
    expect(screen.queryByText(/本次跳过/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /重试失败项/ })).not.toBeInTheDocument();
  });
  it('opens metadata details and disables links for deleted documents', async () => {
    const batch = operation('metadata'); const onOpen = vi.fn();
    vi.mocked(getAcademicImportBatch).mockResolvedValue({ operation: batch, items: [paper('metadata', { current_document: undefined, library_paper_id: 'local-paper', current_state: 'metadata_only', result_kind: 'metadata_only' }), paper('deleted', { current_document: undefined, document_deleted: true, current_state: 'deleted' })], next_cursor: '' });
    render(<Notice canWrite onRetry={vi.fn()} onOpenPaper={onOpen} page={{ ...emptyImportBatchPage(), items: [batch] }} />); openNotice();
    fireEvent.click(screen.getByRole('button', { name: /^展开导入记录/ })); const title = await screen.findByRole('button', { name: 'Paper metadata' });
    expect(screen.queryByRole('link', { name: 'Paper deleted' })).not.toBeInTheDocument(); expect(screen.getByText('文档已删除')).toBeInTheDocument();
    fireEvent.click(title); expect(onOpen).toHaveBeenCalledWith('local-paper');
  });
  it('retries only the selected operation once', async () => {
    let finish!: () => void; const retry = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    const batch = operation('failed'); batch.summary.retryable = 1; batch.summary.needs_action = 1;
    render(<Notice canWrite onRetry={retry} page={{ ...emptyImportBatchPage(), items: [batch], notification: { active: 0, needs_action: 1 } }} />); openNotice();
    const button = screen.getByRole('button', { name: '重试失败项（1）' }); fireEvent.click(button); fireEvent.click(button);
    expect(retry).toHaveBeenCalledOnce(); expect(retry).toHaveBeenCalledWith('failed'); expect(button).toBeDisabled(); await act(async () => finish());
  });
  it('uses complete notification totals and keeps resolved historical failures visible to read-only users', async () => {
    const batch = operation('failed', { status: 'failed' });
    batch.summary = { ...batch.summary, total: 1, ready: 0, skipped: 0, failed: 1, resolved_later: 1 };
    vi.mocked(getAcademicImportBatch).mockResolvedValue({ operation: batch, items: [paper('failed', { status: 'failed', current_state: 'failed', resolved_later: true, error_message: 'HTTP 503', can_retry: false })], next_cursor: '' });
    render(<Notice canWrite={false} onRetry={vi.fn()} page={{ ...emptyImportBatchPage(), items: [batch], notification: { active: 2, needs_action: 0 } }} />);
    expect(screen.getByRole('button', { name: '通知：2 项进行中 · 0 条待处理' })).toBeInTheDocument(); openNotice();
    expect(screen.getByRole('status')).toHaveTextContent('2 项进行中');
    expect(screen.getByRole('status')).not.toHaveTextContent('待处理');
    expect(screen.queryByRole('button', { name: /重试失败项/ })).not.toBeInTheDocument(); fireEvent.click(screen.getByRole('button', { name: /^展开导入记录/ }));
    expect(await screen.findByText('已通过后续操作入库')).toBeInTheDocument();
    expect(screen.getByText('失败（已解决）')).toBeInTheDocument();
    expect(screen.getByText('导入失败（已解决）')).toBeInTheDocument();
  });
  it('keeps a failed batch as one pending notice when loading its details fails and retries the read', async () => {
    const batch = operation('failed'); batch.summary.needs_action = 1;
    vi.mocked(getAcademicImportBatch).mockRejectedValueOnce(new Error('details unavailable')).mockResolvedValue({ operation: batch, items: [paper('recovered')], next_cursor: '' });
    render(<Notice canWrite onRetry={vi.fn()} page={{ ...emptyImportBatchPage(), items: [batch], notification: { active: 0, needs_action: 1 } }} />);
    openNotice();
    fireEvent.click(screen.getByRole('button', { name: /^展开导入记录/ }));
    expect(await screen.findByText('导入详情加载失败')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /通知：.*1 条待处理/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByRole('link', { name: 'Paper recovered' })).toBeInTheDocument();
    expect(screen.queryByText('导入详情加载失败')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /通知：.*1 条待处理/ })).toBeInTheDocument();
  });
  it('defaults to pending and does not invent an empty history when synchronization failed', async () => {
    const refresh = vi.fn();
    const view = render(<Notice canWrite onRetry={vi.fn()} onRefresh={refresh} syncError="HTTP 404" page={emptyImportBatchPage()} />);
    openNotice();
    expect(screen.getByRole('tab', { name: '待处理 1' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('导入记录同步失败')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '导入记录' }));
    expect(screen.queryByText('暂无论文导入记录')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '待处理 1' }));
    fireEvent.click(screen.getByRole('button', { name: '重新同步' }));
    expect(refresh).toHaveBeenCalledOnce();
    view.rerender(<Notice canWrite onRetry={vi.fn()} page={emptyImportBatchPage()} />);
    await waitFor(() => expect(screen.getByRole('button', { name: /通知：.*0 条待处理/ })).toBeInTheDocument());
  });
  it('loads more operations and papers while keeping the complete batch count', async () => {
    const batch = operation('batch'); batch.summary.total = 205;
    vi.mocked(listAcademicImportBatches).mockResolvedValue({ ...emptyImportBatchPage(), items: [operation('second', { entry_type: 'pdf_upload', source: { file_name: 'study.pdf' } })] });
    vi.mocked(getAcademicImportBatch).mockResolvedValueOnce({ operation: batch, items: [paper('first')], next_cursor: 'papers-next' }).mockResolvedValueOnce({ operation: batch, items: [paper('second')], next_cursor: '' });
    render(<Notice canWrite onRetry={vi.fn()} page={{ ...emptyImportBatchPage(), items: [batch], next_cursor: 'batches-next' }} />); openNotice();
    fireEvent.click(screen.getByRole('button', { name: '加载更多记录' })); expect(await screen.findByText('上传 PDF · 2 篇')).toBeInTheDocument();
    await waitFor(() => expect(screen.getAllByRole('button', { name: /^展开导入记录/ })[0]).not.toBeDisabled());
    fireEvent.click(screen.getAllByRole('button', { name: /^展开导入记录/ })[0]);
    await screen.findByRole('link', { name: 'Paper first' });
    expect(getAcademicImportBatch).toHaveBeenCalledTimes(1);
    fireEvent.click(await screen.findByRole('button', { name: '加载更多论文' }));
    expect(await screen.findByRole('link', { name: 'Paper second' })).toBeInTheDocument(); expect(screen.getByText('扩展导入 · 205 篇')).toBeInTheDocument();
    expect(getAcademicImportBatch).toHaveBeenLastCalledWith('batch', 'papers-next');
  });
});
