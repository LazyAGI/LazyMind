import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MDXEditor, imagePlugin } from '@mdxeditor/editor';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { writerImageDownloadPlugin } from './writerImageDownloadPlugin';

const mocks = vi.hoisted(() => ({ download: vi.fn(), error: vi.fn() }));
vi.mock('@/modules/chat/utils/download', () => ({ downloadStream: mocks.download }));
vi.mock('antd', () => ({ message: { error: mocks.error } }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

const markdown = 'An illustration\n\n![Coast](assets/coast.jpg)';
const resolveImage = vi.fn(async () => '/api/core/static-files/coast.jpg?signed=example');
const label = 'chat.writerImages.downloadOne';
function Editor({ onChange = vi.fn(), onDownload }: { onChange?: (markdown: string) => void; onDownload?: (source: string) => Promise<void> }) {
  return <MDXEditor markdown={markdown} onChange={onChange}
    plugins={[imagePlugin({ imagePreviewHandler: resolveImage }), writerImageDownloadPlugin({ onDownload })]} />;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('Image', class {
    onload?: () => void;
    set src(_value: string) { queueMicrotask(() => this.onload?.()); }
  });
  vi.stubGlobal('fetch', vi.fn());
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('Markdown image download toolbar', () => {
  it('uses the document export handler without requiring cross-origin browser access', async () => {
    let finish!: () => void;
    const onDownload = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
    render(<Editor onDownload={onDownload} />);
    const button = await screen.findByRole('button', { name: label });
    fireEvent.click(button); fireEvent.click(button);
    await waitFor(() => expect(onDownload).toHaveBeenCalledWith('assets/coast.jpg'));
    expect(onDownload).toHaveBeenCalledTimes(1);
    expect(button).toBeDisabled();
    expect(fetch).not.toHaveBeenCalled();
    await act(async () => finish());
    expect(button).toBeEnabled();
  });

  it('downloads the current source after an image URL is edited', async () => {
    const onDownload = vi.fn(async () => {});
    render(<Editor onDownload={onDownload} />);
    await screen.findByRole('button', { name: label });
    fireEvent.click(screen.getByTitle('Edit image'));
    fireEvent.change(await screen.findByDisplayValue('assets/coast.jpg'), { target: { value: 'https://example.org/replacement.jpg' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(resolveImage).toHaveBeenCalledWith('https://example.org/replacement.jpg'));
    fireEvent.click(screen.getByRole('button', { name: label }));
    await waitFor(() => expect(onDownload).toHaveBeenCalledWith('https://example.org/replacement.jpg'));
  });

  it('downloads the original image without changing the document or losing existing controls', async () => {
    const blob = new Blob(['original jpeg bytes'], { type: 'image/jpeg' });
    vi.mocked(fetch).mockResolvedValue({ ok: true, blob: async () => blob } as Response);
    const onChange = vi.fn();
    const view = render(<Editor onChange={onChange} />);
    const button = await screen.findByRole('button', { name: label });
    onChange.mockClear();
    expect(screen.getByTitle('Delete image')).toBeInTheDocument();
    expect(screen.getByTitle('Edit image')).toBeInTheDocument();
    fireEvent.mouseDown(button); fireEvent.click(button);
    await waitFor(() => expect(mocks.download).toHaveBeenCalledWith(blob, 'coast.jpg'));
    expect(fetch).toHaveBeenCalledWith('/api/core/static-files/coast.jpg?signed=example', expect.objectContaining({ credentials: 'same-origin', signal: expect.any(AbortSignal) }));
    expect(resolveImage).toHaveBeenLastCalledWith('assets/coast.jpg');
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByAltText('Coast')).toBeInTheDocument();
    view.rerender(<Editor onChange={onChange} />);
    expect(screen.getAllByRole('button', { name: label })).toHaveLength(1);
    fireEvent.click(screen.getByTitle('Delete image'));
    await waitFor(() => expect(screen.queryByAltText('Coast')).not.toBeInTheDocument());
    expect(onChange).toHaveBeenCalled();
  });

  it('prevents duplicate requests while preparing and allows retry after failure', async () => {
    let release!: (response: Response) => void;
    vi.mocked(fetch).mockReturnValueOnce(new Promise(resolve => { release = resolve; }));
    render(<Editor />);
    const button = await screen.findByRole('button', { name: label });
    fireEvent.click(button); fireEvent.click(button);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    expect(button).toBeDisabled();
    await act(async () => release({ ok: false } as Response));
    await waitFor(() => expect(mocks.error).toHaveBeenCalledWith('chat.preview.downloadFailed'));
    expect(mocks.download).not.toHaveBeenCalled();
    expect(button).toBeEnabled();
    vi.mocked(fetch).mockResolvedValue({ ok: true, blob: async () => new Blob(['image']) } as Response);
    fireEvent.click(button);
    await waitFor(() => expect(mocks.download).toHaveBeenCalledOnce());
  });

  it('cancels a pending download when leaving the editor', async () => {
    let release!: (response: Response) => void;
    vi.mocked(fetch).mockReturnValue(new Promise(resolve => { release = resolve; }));
    const view = render(<Editor />);
    fireEvent.click(await screen.findByRole('button', { name: label }));
    await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
    const signal = vi.mocked(fetch).mock.calls[0][1]?.signal;
    view.unmount();
    expect(signal?.aborted).toBe(true);
    await act(async () => release({ ok: true, blob: async () => new Blob(['image']) } as Response));
    expect(mocks.download).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });
});
