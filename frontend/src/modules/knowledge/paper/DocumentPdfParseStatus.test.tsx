import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, expect, it, vi } from 'vitest';
import DocumentPdfParseStatus from './DocumentPdfParseStatus';

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: http, getLocalizedErrorMessage: (error: Error) => error.message }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

it('reads shared PDF structure independently of retrieval and submits directly from the badge', async () => {
  const endpoint = '/api/core/datasets/library%20a/documents/pdf%2F1/pdf-ir';
  const document = { version: 1, source_sha256: 'original', coordinate_space: 'normalized_top_left', parser: { name: 'mineru', model: 'vlm' }, pages: [], blocks: [] };
  let state = { status: 'failed', document, error_message: 'MinerU unavailable' };
  http.get.mockImplementation(async (url: string) => {
    if (url !== endpoint) throw Object.assign(new Error('Retrieval unavailable'), { response: { status: 502 } });
    return { data: { data: state } };
  });
  let resolve!: (value: unknown) => void;
  http.post.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
  render(<ConfigProvider theme={{ token: { motion: false } }}><DocumentPdfParseStatus dataset="library a" document="pdf/1" /></ConfigProvider>);
  fireEvent.click(await screen.findByRole('button', { name: 'PDF 解析：解析失败' }));
  expect(await screen.findByText(/已有解析结果仍可使用/)).toBeInTheDocument();
  const action = screen.getByRole('button', { name: '重试解析' });
  fireEvent.click(action);
  fireEvent.click(action);
  expect(http.post).toHaveBeenCalledOnce();
  expect(http.post).toHaveBeenLastCalledWith('/api/core/datasets/library%20a/documents/pdf%2F1/pdf-ir', { force: true }, expect.anything());
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  state = { status: 'queued', document, error_message: '' };
  await act(async () => resolve({ data: { data: { status: 'queued' } } }));
  expect(await screen.findByRole('button', { name: 'PDF 解析：等待解析' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '重试解析' })).not.toBeInTheDocument();
  expect(screen.getByText('已有解析结果仍可使用。')).toBeInTheDocument();
  await waitFor(() => expect(http.get.mock.calls.length).toBeGreaterThan(1));
  expect(http.get.mock.calls.every(([url]) => url === endpoint)).toBe(true);
});

it('distinguishes quota waiting from an executing task and protects read-only documents', async () => {
  const structure = { status: 'queued', available: false, error_code: 'reading_quota_exhausted', error_message: 'PDF 解析额度已用尽' };
  http.get.mockResolvedValue({ data: { data: structure } });
  render(<ConfigProvider theme={{ token: { motion: false } }}><DocumentPdfParseStatus dataset="dataset" document="paper" disabledReason="当前文档为只读" /></ConfigProvider>);
  fireEvent.click(await screen.findByRole('button', { name: 'PDF 解析：等待自动额度' }));
  expect(await screen.findByText('PDF 解析额度已用尽')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '开始解析' })).toBeDisabled();
  expect(screen.getByText('当前文档为只读')).toBeInTheDocument();
  expect(http.post).not.toHaveBeenCalled();
});
