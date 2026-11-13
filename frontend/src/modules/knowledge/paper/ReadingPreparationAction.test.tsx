import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { usePdfIR } from '../api/pdfIR';
import { ConfigProvider } from 'antd';
import ReadingPreparationAction from './ReadingPreparationAction';
vi.mock('./PaperPreparationStatus', () => ({ default: () => <div>论文解析状态</div> }));
const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
const api = vi.hoisted(() => ({ get: vi.fn(), prepare: vi.fn() }));
vi.mock('../api/readingPreparation', () => ({ getReadingPreparation: api.get, prepareReading: api.prepare }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http, getLocalizedErrorMessage: (e: Error) => e.message }));
afterEach(() => { cleanup(); vi.useRealTimers(); vi.clearAllMocks(); });
it('opens without paid work, explicitly submits chosen scopes, and polls to terminal failure', async () => {
  vi.useFakeTimers();
  api.get.mockResolvedValueOnce({ status: 'not_requested' }).mockResolvedValueOnce({ status: 'running', progress_current: 1, progress_total: 2 }).mockResolvedValue({ status: 'failed', error_message: '服务暂时不可用', model_requests: 1 });
  api.prepare.mockResolvedValueOnce({ status: 'pending' });
  render(<ReadingPreparationAction target={{ dataset: 'ds', document: 'doc', canWrite: true }} />);
  fireEvent.click(screen.getByRole('button', { name: '研读论文' }));
  await act(async () => { await Promise.resolve(); });
  expect(api.prepare).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText('选择本篇准备内容'));
  fireEvent.click(screen.getByRole('checkbox', { name: '公式研读' }));
  fireEvent.click(screen.getByRole('button', { name: /开始准备/ }));
  await act(async () => { await Promise.resolve(); });
  expect(api.prepare).toHaveBeenCalledWith('ds', 'doc', ['pdf_parse', 'formula']);
  expect(screen.getByRole('button', { name: /开始准备/ })).toBeDisabled();
  await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
  expect(screen.getByText('服务暂时不可用')).toBeInTheDocument();
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  expect(api.get).toHaveBeenCalledTimes(4);
  expect(api.prepare).toHaveBeenCalledOnce();
});

it('refreshes observed artifacts once after preparation succeeds without generation or remount', async () => {
  vi.useFakeTimers();
  http.get.mockResolvedValueOnce({ data: { data: { status: 'not_requested' } } }).mockResolvedValue({ data: { data: { status: 'ready' } } });
  api.get.mockReset().mockResolvedValueOnce({ status: 'not_requested' }).mockResolvedValueOnce({ job_id: 'new-job', status: 'running' }).mockResolvedValue({ job_id: 'new-job', status: 'succeeded' });
  api.prepare.mockResolvedValueOnce({ job_id: 'new-job', status: 'pending' });
  function Reader() {
    const ir = usePdfIR({ dataset: 'ds', document: 'doc' });
    return <><button disabled={ir.state.status !== 'ready'}>研读工具</button><ReadingPreparationAction target={{ dataset: 'ds', document: 'doc' }} onPrepared={ir.refresh} /></>;
  }
  render(<Reader />);
  await act(async () => {});
  expect(screen.getByRole('button', { name: '研读工具' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '研读论文' }));
  await act(async () => {});
  fireEvent.click(screen.getByRole('button', { name: /开始准备/ }));
  await act(async () => {});
  await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
  expect(screen.getByRole('button', { name: '研读工具' })).toBeEnabled();
  expect(http.get).toHaveBeenCalledTimes(2);
  expect(http.post).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: /关\s*闭/ }));
  fireEvent.click(screen.getByRole('button', { name: '研读论文' }));
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  expect(http.get).toHaveBeenCalledTimes(2);
  expect(api.prepare).toHaveBeenCalledOnce();
});
it('does not refresh for an old completed job when opened', async () => {
  api.get.mockReset().mockResolvedValue({ job_id: 'old-job', status: 'succeeded' });
  const refresh = vi.fn();
  render(<ReadingPreparationAction target={{ dataset: 'ds', document: 'doc' }} onPrepared={refresh} />);
  fireEvent.click(screen.getByRole('button', { name: '研读论文' }));
  await act(async () => {});
  expect(refresh).not.toHaveBeenCalled();
});

it('observes an active job to completion after progress closes and refreshes exactly once', async () => {
  vi.useFakeTimers();
  api.get.mockReset().mockResolvedValueOnce({ status: 'not_requested' })
    .mockResolvedValueOnce({ job_id: 'background-job', status: 'running' })
    .mockResolvedValueOnce({ job_id: 'background-job', status: 'running' })
    .mockResolvedValue({ job_id: 'background-job', status: 'succeeded' });
  api.prepare.mockResolvedValueOnce({ job_id: 'background-job', status: 'pending' });
  const refresh = vi.fn();
  render(<ReadingPreparationAction target={{ dataset: 'ds', document: 'doc' }} onPrepared={refresh} />);
  expect(api.get).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: '研读论文' }));
  await act(async () => {});
  fireEvent.click(screen.getByRole('button', { name: /开始准备/ }));
  await act(async () => {});
  fireEvent.click(screen.getByRole('button', { name: /关\s*闭/ }));
  await act(async () => {});
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  expect(refresh).toHaveBeenCalledOnce();
  expect(screen.queryByText('论文解析状态')).not.toBeInTheDocument();
  const reads = api.get.mock.calls.length;
  await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
  expect(api.get).toHaveBeenCalledTimes(reads);
  expect(api.prepare).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole('button', { name: '研读论文' }));
  await act(async () => {});
  expect(refresh).toHaveBeenCalledOnce();
});

it.each(['target change', 'unmount'])('stops closed-dialog observation on %s', async (stop) => {
  vi.useFakeTimers();
  api.get.mockReset().mockResolvedValue({ job_id: 'active-job', status: 'running' });
  const refresh = vi.fn();
  const view = render(<ReadingPreparationAction target={{ dataset: 'ds', document: 'doc' }} onPrepared={refresh} />);
  fireEvent.click(screen.getByRole('button', { name: '研读论文' }));
  await act(async () => {});
  fireEvent.click(screen.getByRole('button', { name: /关\s*闭/ }));
  await act(async () => {});
  if (stop === 'unmount') view.unmount();
  else view.rerender(<ReadingPreparationAction target={{ dataset: 'ds', document: 'another-doc' }} onPrepared={refresh} />);
  const reads = api.get.mock.calls.length;
  await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
  expect(api.get).toHaveBeenCalledTimes(reads);
  expect(refresh).not.toHaveBeenCalled();
  expect(api.prepare).not.toHaveBeenCalled();
});

it('shows preparation choices without an expansion step', async () => {
 api.get.mockReset().mockResolvedValue({status:'not_requested'});
 const view=render(<ConfigProvider theme={{token:{motion:false}}}><ReadingPreparationAction target={{dataset:'ds',document:'doc'}}/></ConfigProvider>);
 fireEvent.click(screen.getByRole('button',{name:'研读论文'}));
 await act(async()=>{});
 await waitFor(() => expect(screen.getByText('公式研读')).toBeVisible());
 expect(view.baseElement.querySelector('details:not([open])')).toBeNull();
});

it('ignores a delayed response from a different document while the dialog stays open', async () => {
 let resolveOld!: (value: unknown) => void;
 api.get.mockReset().mockImplementation((_dataset: string, document: string) => document === 'old' ? new Promise(resolve => {resolveOld=resolve;}) : Promise.resolve({status:'failed',error_message:'当前文档失败'}));
 const view=render(<ReadingPreparationAction target={{dataset:'ds',document:'old'}}/>);
 fireEvent.click(screen.getByRole('button',{name:'研读论文'}));
 await act(async()=>{});
 view.rerender(<ReadingPreparationAction target={{dataset:'ds',document:'new'}}/>);
 await act(async()=>{});
 expect(screen.getByText('当前文档失败')).toBeInTheDocument();
 await act(async()=>{resolveOld({status:'failed',error_message:'旧文档失败'});});
 expect(screen.queryByText('旧文档失败')).not.toBeInTheDocument();
 expect(screen.getByText('当前文档失败')).toBeInTheDocument();
});
