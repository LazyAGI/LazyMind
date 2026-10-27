import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ZoteroImportModal from './ZoteroImportModal';
import { previewZotero } from './zoteroImport';

vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { post: vi.fn() } }));
vi.mock('./zoteroImport', async (original) => ({ ...await original<typeof import('./zoteroImport')>(), previewZotero: vi.fn() }));
const preview = { source_id: 'users:42', collections: [], warnings: [], items: [
  { key: 'PAPER001', data: { key: 'PAPER001', title: 'Real paper', itemType: 'journalArticle', creators: [{ name: 'Ada Lovelace' }], date: '2024' } },
  { key: 'PAPER002', data: { key: 'PAPER002', title: 'Other paper', itemType: 'book', creators: [{ name: 'Grace Hopper' }], date: '2023' } },
  { key: 'PDF00001', data: { key: 'PDF00001', itemType: 'attachment', parentItem: 'PAPER001', contentType: 'application/pdf', filename: 'real.pdf' } },
] };
const onImport = vi.fn();
const onCancel = vi.fn();
function mount(canWrite = true) {
  return render(<ConfigProvider theme={{ token: { motion: false } }}><ZoteroImportModal open datasetId="dataset" canWrite={canWrite} onCancel={onCancel} onImport={onImport} /></ConfigProvider>);
}
async function load() {
  fireEvent.click(screen.getByRole('button', { name: '读取本地数据库' }));
  await screen.findByText('Real paper');
}
function selectPaper() { fireEvent.click(screen.getByRole('checkbox', { name: '选择 Real paper' })); }
beforeEach(() => { vi.resetAllMocks(); localStorage.clear(); vi.mocked(previewZotero).mockResolvedValue(preview); onImport.mockResolvedValue({ imported: 1, attachments: 0 }); });
afterEach(cleanup);

it('offers only database and API entries and reads the database without API credentials or a directory', async () => {
  mount();
  const entries = within(screen.getByRole('radiogroup', { name: '导入来源' }));
  expect(entries.getAllByRole('radio')).toHaveLength(2);
  expect(entries.getByRole('radio', { name: /本地数据库/ })).toBeChecked();
  expect(entries.getByRole('radio', { name: /API/ })).toBeInTheDocument();
  expect(screen.queryByLabelText('API Key')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('本地 API 地址')).not.toBeInTheDocument();
  expect(screen.queryByText('选择本地附件文件夹')).not.toBeInTheDocument();
  await load();
  expect(previewZotero).toHaveBeenCalledWith('dataset', expect.objectContaining({ source: 'database', library_type: 'users', library_id: '0' }), expect.any(AbortSignal));
  expect(onImport).not.toHaveBeenCalled();
});

it('requires a selection and commits only selected papers and their attachments', async () => {
  mount(); await load();
  expect(screen.getByRole('button', { name: /确认导入/ })).toBeDisabled();
  selectPaper();
  expect(screen.getByText('已选择 1 / 2 篇')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /确认导入/ }));
  await waitFor(() => expect(onImport).toHaveBeenCalledOnce());
  const [selected, config, options] = onImport.mock.calls[0];
  expect(selected.items.map((item: { key: string }) => item.key)).toEqual(['PAPER001', 'PDF00001']);
  expect(config).toMatchObject({ source: 'database', library_id: '0' });
  expect(options).toMatchObject({ includeAttachments: true, localFiles: [] });
});

it('shows unavailable local PDFs while keeping them in the confirmed import for visible retry errors', async () => {
  vi.mocked(previewZotero).mockResolvedValue({ ...preview, items: preview.items.map(item => item.key === 'PDF00001' ? { ...item, data: { ...item.data, localFileAvailable: false } } : item) });
  mount(); await load();
  expect(screen.getByText('1 个 PDF 不可用')).toBeInTheDocument();
  selectPaper();
  fireEvent.click(screen.getByRole('button', { name: /确认导入/ }));
  await waitFor(() => expect(onImport).toHaveBeenCalledOnce());
  expect(onImport.mock.calls[0][0].items.find((item: { key: string }) => item.key === 'PDF00001').data.localFileAvailable).toBe(false);
});

it('shows provider and unsupported-material warnings without hiding selectable papers', async () => {
  vi.mocked(previewZotero).mockResolvedValue({ ...preview, warnings: ['Provider notice'], items: [...preview.items,
    { key: 'LONENOTE', data: { key: 'LONENOTE', itemType: 'note', note: 'Standalone note' } },
    { key: 'WEB00001', data: { key: 'WEB00001', itemType: 'attachment', contentType: 'text/html', parentItem: 'PAPER001' } },
  ] });
  mount(); await load();
  const warning = screen.getByRole('alert');
  expect(warning).toHaveTextContent('Provider notice');
  expect(warning).toHaveTextContent('1 条独立笔记没有关联文献，本次不会导入');
  expect(warning).toHaveTextContent('1 个非 PDF 附件本次不会导入');
  expect(screen.getByRole('checkbox', { name: '选择 Real paper' })).toBeEnabled();
});

it('searches titles, authors and years and selects only the search results', async () => {
  mount(); await load();
  const search = screen.getByRole('textbox', { name: '搜索文献' });
  fireEvent.change(search, { target: { value: 'Hopper 2023' } });
  expect(screen.queryByText('Real paper')).not.toBeInTheDocument();
  expect(screen.getByText('Other paper')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '选择搜索结果' }));
  expect(screen.getByText('已选择 1 / 2 篇')).toBeInTheDocument();
  fireEvent.change(search, { target: { value: 'Real' } });
  expect(screen.getByRole('checkbox', { name: '选择 Real paper' })).not.toBeChecked();
  fireEvent.click(screen.getByRole('button', { name: '清空选择' }));
  expect(screen.getByRole('button', { name: /确认导入/ })).toBeDisabled();
});

it('cancels a loaded preview without importing and blocks read-only imports', async () => {
  const mounted = mount(); await load(); selectPaper();
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: /取\s*消/ }));
  expect(onCancel).toHaveBeenCalledOnce(); expect(onImport).not.toHaveBeenCalled();
  mounted.unmount(); mount(false);
  expect(screen.getByRole('button', { name: '读取本地数据库' })).toBeDisabled();
  expect(screen.getByRole('alert')).toHaveTextContent('需要论文库编辑权限');
});

it('aborts a pending preview on cancel and ignores its late response', async () => {
  let finish!: (value: typeof preview) => void;
  vi.mocked(previewZotero).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  mount();
  fireEvent.click(screen.getByRole('button', { name: '读取本地数据库' }));
  await waitFor(() => expect(previewZotero).toHaveBeenCalledOnce());
  const cancel = within(screen.getByRole('dialog')).getByRole('button', { name: /取\s*消/ });
  expect(cancel).toBeEnabled();
  fireEvent.click(cancel);
  expect(vi.mocked(previewZotero).mock.calls[0][2]?.aborted).toBe(true);
  expect(onCancel).toHaveBeenCalledOnce();
  await act(async () => finish(preview));
  expect(screen.queryByText('Real paper')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /确认导入/ })).not.toBeInTheDocument();
  expect(onImport).not.toHaveBeenCalled();
});

it('keeps provider and partial-import errors visible and retries the same selection', async () => {
  mount();
  vi.mocked(previewZotero).mockRejectedValueOnce(new Error('找不到本地 zotero.sqlite'));
  fireEvent.click(screen.getByRole('button', { name: '读取本地数据库' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('找不到本地 zotero.sqlite');
  await load(); selectPaper();
  onImport.mockResolvedValue({ imported: 1, attachments: 0, failures: ['real.pdf: download failed'] });
  fireEvent.click(screen.getByRole('button', { name: /确认导入/ }));
  expect(await screen.findByText(/real.pdf: download failed/)).toBeInTheDocument();
  expect(onCancel).not.toHaveBeenCalled();
  expect(screen.getByRole('checkbox', { name: '选择 Real paper' })).toBeChecked();
  fireEvent.click(screen.getByRole('button', { name: '重试未完成的导入' }));
  await waitFor(() => expect(onImport).toHaveBeenCalledTimes(2));
  expect(onImport.mock.calls[1][0]).toEqual(onImport.mock.calls[0][0]);
});

it('keeps API modes available and clears credentials and selection when changing sources', async () => {
  mount(); await load(); selectPaper();
  fireEvent.click(screen.getByRole('radio', { name: /API 通过/ }));
  expect(screen.queryByText('Real paper')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('radio', { name: '远端 Zotero' }));
  fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'secret-key' } });
  fireEvent.click(screen.getByRole('radio', { name: 'WebDAV' }));
  expect(screen.getByLabelText('API Key')).toHaveValue('');
  expect(screen.getByText(/WebDAV 只提供附件/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('radio', { name: /本地数据库/ }));
  expect(screen.queryByLabelText('API Key')).not.toBeInTheDocument();
  expect(localStorage.length).toBe(0);
});

it('invalidates selection when the detected database library changes', async () => {
  vi.mocked(previewZotero).mockResolvedValue({ ...preview, database: { path: '/data/zotero.sqlite', library_type: 'users', library_id: '0', libraries: [{ type: 'users', id: '0', name: '我的文献库' }, { type: 'groups', id: '17', name: 'Research group' }] } });
  mount(); await load(); selectPaper();
  fireEvent.mouseDown(screen.getByRole('combobox', { name: '本地文献库' }));
  fireEvent.click(await screen.findByText('Research group'));
  expect(screen.queryByText('Real paper')).not.toBeInTheDocument();
  await load();
  expect(previewZotero).toHaveBeenLastCalledWith('dataset', expect.objectContaining({ source: 'database', library_type: 'groups', library_id: '17' }), expect.any(AbortSignal));
  expect(screen.getByRole('checkbox', { name: '选择 Real paper' })).not.toBeChecked();
});

it.each([
  ['本地客户端', 'local'], ['远端 Zotero', 'remote'], ['WebDAV', 'webdav'],
])('keeps %s API previews available without writing before selection', async (label, source) => {
  mount();
  fireEvent.click(screen.getByRole('radio', { name: /API 通过/ }));
  fireEvent.click(screen.getByRole('radio', { name: label }));
  if (source !== 'local') fireEvent.change(screen.getByLabelText(/^用户 ID/), { target: { value: '42' } });
  if (source === 'webdav') fireEvent.change(screen.getByLabelText('WebDAV 地址'), { target: { value: 'https://dav.example.com/zotero' } });
  fireEvent.click(screen.getByRole('button', { name: '连接并预览' }));
  await screen.findByText('Real paper');
  expect(previewZotero).toHaveBeenCalledWith('dataset', expect.objectContaining({ source, library_id: source === 'local' ? '0' : '42' }), expect.any(AbortSignal));
  expect(onImport).not.toHaveBeenCalled();
  expect(screen.getByRole('button', { name: /确认导入/ })).toBeDisabled();
});

it('prevents source changes and closing while import is running', async () => {
  let finish!: (value: { imported: number; attachments: number }) => void;
  onImport.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  mount(); await load(); selectPaper();
  fireEvent.click(screen.getByRole('button', { name: /确认导入/ }));
  await waitFor(() => expect(screen.getByRole('radio', { name: /API 通过/ })).toBeDisabled());
  expect(within(screen.getByRole('dialog')).getByRole('button', { name: /取\s*消/ })).toBeDisabled();
  await act(async () => finish({ imported: 1, attachments: 0 }));
  await waitFor(() => expect(onCancel).toHaveBeenCalledOnce());
});
