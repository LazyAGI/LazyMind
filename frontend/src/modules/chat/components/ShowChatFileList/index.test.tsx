import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ShowChatFileList from './index';
import type { ChatFileList } from '../ChatInput/types';

afterEach(() => vi.unstubAllGlobals());

describe('composer attachment image previews', () => {
  it.each([false, true])('resolves a restored image for its thumbnail and preview with mixed files=%s', async mixed => {
    const storedPath = `/var/lib/lazymind/uploads/tmp/users/test/${mixed}/image.png`;
    const signedUrl = `/api/core/static-files/tmp/users/test/${mixed}/image.png?expires=4102444800&sig=test`;
    let finishSigning!: (response: Response) => void;
    const fetch = vi.fn(() => new Promise<Response>(resolve => { finishSigning = resolve; }));
    vi.stubGlobal('fetch', fetch);
    const image: ChatFileList = { uid: 'upload:image', name: 'image.png', suffix: '.png', size: '', base64: '', previewUrl: storedPath };
    const files = mixed ? [image, { uid: 'upload:pdf', name: 'paper.pdf', suffix: '.pdf', size: '', base64: '' }] : [image];
    const onRemove = vi.fn();
    const view = render(<ShowChatFileList fileList={files} onRemove={onRemove} />);

    // Storage references are not readable browser URLs, even while signing.
    expect(screen.getByAltText('image.png')).not.toHaveAttribute('src', storedPath);
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining('/api/core/static-files:sign'), expect.objectContaining({
      method: 'POST', body: JSON.stringify({ paths: [storedPath] }),
    })));
    await act(async () => finishSigning(new Response(JSON.stringify({ urls: { [storedPath]: signedUrl } }), {
      headers: { 'Content-Type': 'application/json' },
    })));
    await waitFor(() => expect(screen.getByAltText('image.png')).toHaveAttribute('src', signedUrl));
    fireEvent.click(screen.getByAltText('image.png'));
    await waitFor(() => expect(document.querySelector('.ant-image-preview-img')).toHaveAttribute('src', signedUrl));
    view.unmount();
  });

  it.each(['data:image/png;base64,cGljdHVyZQ==', 'blob:local-picture'])('keeps a local preview without signing: %s', src => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    render(<ShowChatFileList fileList={[{ uid: 'local', name: 'image.png', suffix: '.png', size: '', base64: src }]} onRemove={vi.fn()} />);
    expect(screen.getByAltText('image.png')).toHaveAttribute('src', src);
    expect(fetch).not.toHaveBeenCalled();
  });
});

it('resolves restored PDF storage references before opening', async () => {
  const storedPath = '/var/lib/lazymind/uploads/tmp/users/test/paper.pdf';
  const signedUrl = '/api/core/static-files/tmp/users/test/paper.pdf?expires=4102444800&sig=test';
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ urls: { [storedPath]: signedUrl } }), { headers: {'Content-Type':'application/json'} })));
  const open = vi.spyOn(window, 'open').mockImplementation(() => null);
  try {
    render(<ShowChatFileList fileList={[{ uid: 'upload:pdf', name: 'paper.pdf', suffix: '.pdf', size: '', base64: '', previewUrl: storedPath }]} onRemove={vi.fn()} />);
    await waitFor(() => expect(screen.getByRole('button', { name: /paper.pdf/ })).toHaveAttribute('aria-disabled', 'false'));
    fireEvent.click(screen.getByRole('button', { name: /paper.pdf/ }));
    await waitFor(() => expect(open).toHaveBeenCalledWith(signedUrl, '_blank', 'noopener,noreferrer'));
  } finally { open.mockRestore(); }
});

it('opens local PDF previews synchronously without requesting a signature', () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  const open = vi.spyOn(window, 'open').mockImplementation(() => null);
  try {
    render(<ShowChatFileList fileList={[{ uid: 'local-pdf', name: 'local.pdf', suffix: '.pdf', size: '', base64: '', previewUrl: 'blob:local-pdf' }]} onRemove={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: /local.pdf/ }));
    expect(open).toHaveBeenCalledWith('blob:local-pdf', '_blank', 'noopener,noreferrer');
    expect(fetch).not.toHaveBeenCalled();
  } finally { open.mockRestore(); }
});
