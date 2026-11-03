import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { DocumentImageExportState } from '@/api/generated/core-client';
import { useDocumentImageDownloads, type DocumentImageDownloadSource } from './useDocumentImageDownloads';

const api = vi.hoisted(() => ({ createDocumentImageExport: vi.fn(), readDocumentImageExport: vi.fn(), retryDocumentImageExport: vi.fn(), downloadDocumentImages: vi.fn() }));
const download = vi.hoisted(() => vi.fn());
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => api }));
vi.mock('@/modules/chat/utils/download', () => ({ downloadStream: download }));
const resolve = async (source: string) => source;
const source: DocumentImageDownloadSource = { artifactId: 'article', revision: 2, draft: 3, document: '![Photo](https://example.org/photo.png)' };
function Harness({ scope = 'article', current = source }: { scope?: string; current?: DocumentImageDownloadSource }) {
  const actions = useDocumentImageDownloads({ scopeKey: scope, readSource: () => current, resolveImage: resolve });
  return <ConfigProvider theme={{ token: { motion: false } }}>{actions.menu.map(item => <button key={item.key} disabled={item.disabled} aria-busy={actions.busy} onClick={item.onClick}>{item.label}</button>)}
    <div onClick={actions.onImageClick}><img alt='Article illustration' src='https://example.org/photo.png' /></div>
    {actions.dialog}</ConfigProvider>;
}
function state(overrides: Partial<DocumentImageExportState> = {}): DocumentImageExportState {
  return { id: 'export', status: 'ready', total: 1, completed: 1, ready: 1, single: false, filename: 'Article-images.zip', expires_at: '2099-01-01T00:00:00Z', items: [{ id: 'image-001', filename: '001-Photo.png', status: 'ready' }], ...overrides };
}
const response = (value: DocumentImageExportState) => ({ data: { data: value } });
beforeEach(async () => {
  vi.clearAllMocks(); await i18n.changeLanguage('en-US');
  api.createDocumentImageExport.mockResolvedValue(response(state()));
  api.downloadDocumentImages.mockResolvedValue({ data: new Blob(['zip']) });
});
afterEach(cleanup);

describe('article image download interactions', () => {
  it('captures the current editor content and suppresses duplicate submissions', async () => {
    let finish!: (value: ReturnType<typeof response>) => void;
    api.createDocumentImageExport.mockReturnValue(new Promise(done => { finish = done; }));
    const view = render(<Harness />);
    fireEvent.click(screen.getByText('Download all images'));
    expect(screen.getByText('Download all images')).toBeDisabled();
    expect(screen.getByText('Download all images')).toHaveAttribute('aria-busy', 'true');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Download all images'));
    view.rerender(<Harness current={{ ...source, document: '# Newer edits' }} />);
    expect(api.createDocumentImageExport).toHaveBeenCalledTimes(1);
    expect(api.createDocumentImageExport).toHaveBeenCalledWith('article', expect.objectContaining({ document: source.document, base_revision: 2, base_draft_version: 3 }), expect.anything());
    await act(async () => finish(response(state())));
    await waitFor(() => expect(download).toHaveBeenCalledWith(expect.any(Blob), 'Article-images.zip'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByText('Download all images')).toBeEnabled();
  });

  it('does not silently download partial results and retries the same export', async () => {
    api.createDocumentImageExport.mockResolvedValue(response(state({ status: 'partial', total: 2, items: [
      { id: 'image-001', filename: '001-Photo.png', status: 'ready' }, { id: 'image-002', filename: 'Second', status: 'failed', error_code: 'IMAGE_UNAVAILABLE' },
    ] })));
    api.retryDocumentImageExport.mockResolvedValue(response(state({ id: 'retry', total: 2, completed: 2, ready: 2 })));
    render(<Harness />); fireEvent.click(screen.getByText('Download all images'));
    await screen.findByText('1 / 2 images ready');
    expect(download).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry / refresh status' }));
    await waitFor(() => expect(api.retryDocumentImageExport).toHaveBeenCalledWith('article', 'export', expect.anything()));
    await waitFor(() => expect(download).toHaveBeenCalledTimes(1));
    expect(api.createDocumentImageExport).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('can explicitly download the available subset', async () => {
    api.createDocumentImageExport.mockResolvedValue(response(state({ status: 'partial', total: 2 })));
    render(<Harness />); fireEvent.click(screen.getByText('Download all images'));
    fireEvent.click(await screen.findByRole('button', { name: 'Download 1 ready images' }));
    await waitFor(() => expect(api.downloadDocumentImages).toHaveBeenCalledWith('article', 'export', 'bundle', expect.anything()));
  });

  it('supports single image preview and downloading the original reference', async () => {
    api.createDocumentImageExport.mockResolvedValue(response(state({ single: true, filename: '001-Photo.png' })));
    render(<Harness />);
    expect(screen.queryByRole('button', { name: 'View article images' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByAltText('Article illustration'));
    expect(await screen.findByAltText('Photo')).toHaveAttribute('src', 'https://example.org/photo.png');
    fireEvent.click(screen.getByRole('button', { name: 'Download original' }));
    await waitFor(() => expect(api.createDocumentImageExport).toHaveBeenCalledWith('article', expect.objectContaining({ image_reference: 'https://example.org/photo.png' }), expect.anything()));
    await waitFor(() => expect(api.downloadDocumentImages).toHaveBeenCalledWith('article', 'export', 'image-001', expect.anything()));
  });

  it('ignores a late response after switching articles', async () => {
    let finish!: (value: ReturnType<typeof response>) => void;
    api.createDocumentImageExport.mockReturnValue(new Promise(done => { finish = done; }));
    const view = render(<Harness />); fireEvent.click(screen.getByText('Download all images'));
    view.rerender(<Harness scope='different' current={{ ...source, artifactId: 'different' }} />);
    await act(async () => finish(response(state())));
    expect(download).not.toHaveBeenCalled(); expect(api.downloadDocumentImages).not.toHaveBeenCalled();
  });

  it('reports stale drafts without silently downloading an earlier article', async () => {
    api.createDocumentImageExport.mockRejectedValue({ response: { data: { data: { code: 'DRAFT_VERSION_CONFLICT' } } } });
    render(<Harness />); fireEvent.click(screen.getByText('Download all images'));
    await screen.findByText('The article has changed. Start a new image download.');
    expect(download).not.toHaveBeenCalled();
  });

  it('recognizes an expired download returned as a JSON blob and offers a fresh export', async () => {
    const body = JSON.stringify({ data: { code: 'IMAGE_EXPORT_EXPIRED' } });
    const blob = Object.assign(new Blob([body], { type: 'application/json' }), { text: async () => body });
    api.downloadDocumentImages.mockRejectedValueOnce({ response: { status: 410, data: blob } });
    render(<Harness />); fireEvent.click(screen.getByText('Download all images'));
    await screen.findByText('This download has expired. Start a new image download.');
    expect(screen.queryByRole('button', { name: 'Retry / refresh status' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Download again' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Download current article images' }));
    await waitFor(() => expect(api.createDocumentImageExport).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(download).toHaveBeenCalledTimes(1));
  });

  it('ignores a blob error decoded after switching articles', async () => {
    let finish!: (value: string) => void;
    const text = vi.fn(() => new Promise<string>(resolve => { finish = resolve; }));
    const blob = Object.assign(new Blob(), { text });
    api.downloadDocumentImages.mockRejectedValueOnce({ response: { data: blob } });
    const view = render(<Harness />); fireEvent.click(screen.getByText('Download all images'));
    await waitFor(() => expect(text).toHaveBeenCalledTimes(1));
    view.rerender(<Harness scope='different' current={{ ...source, artifactId: 'different' }} />);
    await act(async () => finish(JSON.stringify({ data: { code: 'IMAGE_EXPORT_EXPIRED' } })));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(download).not.toHaveBeenCalled();
    expect(screen.getByText('Download all images')).toBeEnabled();
  });
});
