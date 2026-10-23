import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ZoteroImportModal from './ZoteroImportModal';
import { previewZotero } from './zoteroImport';

vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { post: vi.fn() } }));
vi.mock('./zoteroImport', async (original) => ({ ...await original<typeof import('./zoteroImport')>(), previewZotero: vi.fn() }));
const preview = { source_id: 'users:42', collections: [], warnings: [], items: [{ key: 'PAPER001', data: { key: 'PAPER001', title: 'Real paper', itemType: 'journalArticle' } }] };
const onImport = vi.fn();
const onCancel = vi.fn();
function mount() {
  return render(<ConfigProvider theme={{ token: { motion: false } }}><ZoteroImportModal open datasetId="dataset" canWrite onCancel={onCancel} onImport={onImport} /></ConfigProvider>);
}
beforeEach(() => { vi.resetAllMocks(); localStorage.clear(); vi.mocked(previewZotero).mockResolvedValue(preview); onImport.mockResolvedValue({ imported: 1, attachments: 0 }); });
afterEach(cleanup);

it('requires a preview before importing and displays fetched counts', async () => {
  mount();
  expect(screen.queryByRole('button', { name: '确认导入' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '连接并预览' }));
  expect(await screen.findByText('Real paper')).toBeInTheDocument();
  expect(onImport).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: '确认导入' }));
  await waitFor(() => expect(onImport).toHaveBeenCalledWith(preview, expect.objectContaining({ source: 'local', library_id: '0' }), expect.objectContaining({ includeAttachments: true, localFiles: [] })));
});

it('keeps provider and partial-import errors visible and allows retry without claiming success', async () => {
  mount();
  vi.mocked(previewZotero).mockRejectedValueOnce(new Error('Zotero permission denied'));
  fireEvent.click(screen.getByRole('button', { name: '连接并预览' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Zotero permission denied');
  fireEvent.click(screen.getByRole('button', { name: '连接并预览' }));
  await screen.findByText('Real paper');
  onImport.mockResolvedValue({ imported: 1, attachments: 0, failures: ['paper.pdf: download failed'] });
  fireEvent.click(screen.getByRole('button', { name: '确认导入' }));
  expect(await screen.findByText(/paper.pdf: download failed/)).toBeInTheDocument();
  expect(onCancel).not.toHaveBeenCalled();
  expect(screen.getByRole('button', { name: '重试未完成的导入' })).toBeEnabled();
});

it('clears API credentials when changing sources and never persists them', async () => {
  mount();
  fireEvent.click(screen.getByRole('radio', { name: /远端 Zotero/ }));
  fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'secret-key' } });
  fireEvent.click(screen.getByRole('radio', { name: /WebDAV/ }));
  expect(screen.getByLabelText('API Key')).toHaveValue('');
  expect(screen.getByText(/WebDAV 只提供附件/)).toBeInTheDocument();
  expect(localStorage.length).toBe(0);
});

it('prevents source changes and closing while import is running', async () => {
  let finish!: (value: { imported: number; attachments: number }) => void;
  onImport.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  mount();
  fireEvent.click(screen.getByRole('button', { name: '连接并预览' }));
  await screen.findByText('Real paper');
  fireEvent.click(screen.getByRole('button', { name: '确认导入' }));
  await waitFor(() => expect(screen.getByRole('radio', { name: /远端 Zotero/ })).toBeDisabled());
  expect(within(screen.getByRole('dialog')).getByRole('button', { name: /取\s*消/ })).toBeDisabled();
  await act(async () => finish({ imported: 1, attachments: 0 }));
  await waitFor(() => expect(onCancel).toHaveBeenCalledOnce());
});
