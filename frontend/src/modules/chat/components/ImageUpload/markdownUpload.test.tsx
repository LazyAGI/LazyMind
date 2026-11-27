import { createElement, createRef } from 'react';
import { createRequire } from 'node:module';
import { webcrypto } from 'node:crypto';
import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ImageUpload, { allowedTextTypes, type ImageUploadImperativeProps } from '.';

// Keep Ant Design, the chunk uploader and the Desktop bridge real; fake only IPC and Core HTTP.
const { createDesktopBridge } = createRequire(import.meta.url)('../../../../../../desktop/electron/src/preload.js');
const http = vi.hoisted(() => ({ post: vi.fn(), put: vi.fn() }));
vi.mock('@/components/request', () => ({ axiosInstance: http, localizeErrorCode: (value: string) => value }));
vi.mock('@/runtime/apiBase', () => ({ coreApiUrl: (value: string) => '/api/core/' + value }));

function readText(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = reject;
    reader.readAsText(blob);
  });
}
function dependencies(text: string) {
  const encoded = /lazymind-markdown-images:v1:([A-Za-z0-9+/=]+)/.exec(text)?.[1];
  return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(encoded!), value => value.charCodeAt(0))));
}
const body = '# Original\n\n![Local proof](images/local-proof.png)\n';
const image = btoa('owned image bytes');

beforeEach(() => {
  // jsdom lacks Blob.text; use FileReader for both native-looking Files and Ant Design's clones.
  vi.stubGlobal('File', class extends File { text() { return readText(this); } });
  vi.stubGlobal('crypto', webcrypto);
  http.post.mockReset().mockImplementation(async (url: string) => {
    if (url.endsWith('/workflow-input-resources')) return { data: { contract_version: 'workflow.v1', request_id: 'fixture', ok: true, result: { resource_id: 'owned-image' } } };
    if (url.endsWith(':initUpload')) return { data: { upload_id: 'markdown-upload', total_parts: 1, part_size: 5 << 20 } };
    if (url.endsWith(':complete')) return { data: { stored_path: '/uploads/material.md' } };
    throw new Error('Unexpected HTTP call: ' + url);
  });
  http.put.mockReset().mockResolvedValue({ data: {} });
});
afterEach(() => { cleanup(); delete window.lazymindDesktop; vi.unstubAllGlobals(); });

it('preserves each picker File through real Ant Design cloning and imports its local image', async () => {
  const originals = [new File([body], 'material.md', { type: 'text/markdown' }),
    new File([body], 'material.md', { type: 'text/markdown' })];
  const paths = new Map(originals.map((file, index) => [file, `/local/source-${index}/material.md`]));
  const getPathForFile = vi.fn((file: File) => paths.get(file) ?? '');
  const invoke = vi.fn(async () => ({ images: [{ source: 'images/local-proof.png', name: 'local-proof.png',
    mime_type: 'image/png', content_base64: image }], warnings: [] }));
  window.lazymindDesktop = createDesktopBridge({ invoke }, { getPathForFile });
  const ref = createRef<ImageUploadImperativeProps>();
  const onFilesAdded = vi.fn();
  const onBeforeAddFiles = vi.fn((_files: File[], _current: File[]) => null);
  const { container } = render(createElement(ImageUpload, { ref, max: 2, types: allowedTextTypes,
    icon: createElement('span'), updateFiles: vi.fn(), listNum: 0, onFilesAdded, onBeforeAddFiles }));

  for (const [index, original] of originals.entries()) {
    fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files: [original] } });
    await waitFor(() => expect(ref.current?.getFiles().filter(file => file.uri)).toHaveLength(index + 1));
    expect(onFilesAdded.mock.calls[index][0][0]).toBe(original);
    expect(onBeforeAddFiles.mock.calls[index][0][0]).toBe(original);
    expect(getPathForFile.mock.calls[index][0]).toBe(original);
    expect(invoke).toHaveBeenNthCalledWith(index + 1, 'lazymind:collectMarkdownImages', {
      filename: paths.get(original), references: ['images/local-proof.png'],
    });
    const uploaded = await readText(http.put.mock.calls[index][1]);
    expect(uploaded.startsWith(body)).toBe(true);
    expect(dependencies(uploaded)).toEqual({ images: [{ source: 'images/local-proof.png', resource_id: 'owned-image' }], warnings: [] });
    expect(await original.text()).toBe(body);
  }
  expect(http.post.mock.calls.filter(([url]) => url.endsWith('/workflow-input-resources'))).toHaveLength(2);
  expect(http.post).toHaveBeenCalledWith('/api/core/workflow-input-resources', expect.objectContaining({
    name: 'local-proof.png', content_base64: image, content_hash: expect.stringMatching(/^sha256:[a-f0-9]{64}$/),
  }), expect.any(Object));
});

it('finishes the picker upload when native image collection fails', async () => {
  const original = new File([body], 'material.md', { type: 'text/markdown' });
  const getPathForFile = vi.fn((file: File) => file === original ? '/local/material.md' : '');
  const invoke = vi.fn().mockRejectedValue(new Error('native reader unavailable'));
  window.lazymindDesktop = createDesktopBridge({ invoke }, { getPathForFile });
  const ref = createRef<ImageUploadImperativeProps>();
  const { container } = render(createElement(ImageUpload, { ref, max: 1, types: allowedTextTypes,
    icon: createElement('span'), updateFiles: vi.fn(), listNum: 0 }));
  await act(async () => fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files: [original] } }));
  await waitFor(() => expect(ref.current?.getFiles()[0]?.uri).toBe('/uploads/material.md'));
  expect(getPathForFile).toHaveBeenCalledWith(original);
  expect(invoke).toHaveBeenCalledOnce();
  const uploaded = await readText(http.put.mock.calls[0][1]);
  expect(uploaded.startsWith(body)).toBe(true);
  expect(dependencies(uploaded).images).toEqual([]);
  expect(dependencies(uploaded).warnings.join()).toContain('引用图片读取失败或超时');
  expect(http.post.mock.calls.some(([url]) => url.endsWith('/workflow-input-resources'))).toBe(false);
});
