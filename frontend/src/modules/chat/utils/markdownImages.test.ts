import { webcrypto } from 'node:crypto';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { localMarkdownImageSources, prepareMarkdownUpload } from './markdownImages';

const { post } = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock('@/components/request', () => ({ axiosInstance: { post } }));
vi.mock('@/runtime/apiBase', () => ({ coreApiUrl: (value: string) => '/api/core/' + value }));
const data = btoa('image bytes');
function input(text: string) {
  const file = new File([text], 'material.md', { type: 'text/markdown' });
  Object.defineProperty(file, 'text', { value: async () => text });
  return file;
}
function content(file: File): Promise<string> {
  return new Promise(resolve => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.readAsText(file); });
}
function manifest(text: string) {
  const encoded = /lazymind-markdown-images:v1:([A-Za-z0-9+/=]+)/.exec(text)?.[1];
  return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(encoded!), value => value.charCodeAt(0))));
}
afterEach(() => { delete window.lazymindDesktop; vi.unstubAllGlobals(); vi.useRealTimers(); vi.restoreAllMocks(); post.mockReset(); });

describe('Markdown dependency import', () => {
  it('parses inline and reference images, ignoring links, remote images and code', () => {
    expect(localMarkdownImageSources('![a](images/a.png)\n![b][ref]\n\n[ref]: images/b.png\n[link](private.txt)\n![remote](https://x/a.png)\n`![fake](secret.png)`\n```md\n![fake](another.png)\n```'))
      .toEqual(['images/a.png', 'images/b.png']);
  });
  it('discovers local file URIs and Windows absolute paths', () => {
    expect(localMarkdownImageSources('![a](file:///tmp/a.png) ![b](C:/Docs/b.png)'))
      .toEqual(['file:///tmp/a.png', 'C:/Docs/b.png']);
  });
  it('imports a successful image once and retains the exact Markdown body', async () => {
    vi.stubGlobal('crypto', webcrypto);
    window.lazymindDesktop = { collectMarkdownImages: vi.fn(async () => ({ images: [
      { source: 'a.png', name: 'a.png', mime_type: 'image/png', content_base64: data },
      { source: 'b.png', name: 'b.png', mime_type: 'image/png', content_base64: data },
    ], warnings: [] })) };
    post.mockResolvedValue({ data: { contract_version: 'workflow.v1', request_id: 'fixture', ok: true, result: { resource_id: 'image-resource' } } });
    const original = '# Original\n\n![a](a.png) ![b](b.png)\n';
    const result = await content(await prepareMarkdownUpload(input(original)));
    expect(result.startsWith(original + '\n\n<!--')).toBe(true);
    expect(post).toHaveBeenCalledTimes(1);
    expect(manifest(result).images).toEqual([
      { source: 'a.png', resource_id: 'image-resource' }, { source: 'b.png', resource_id: 'image-resource' },
    ]);
  });
  it('keeps successful images and returns the body when another upload fails', async () => {
    vi.stubGlobal('crypto', webcrypto);
    window.lazymindDesktop = { collectMarkdownImages: vi.fn(async () => ({ images: [
      { source: 'a.png', name: 'a.png', mime_type: 'image/png', content_base64: data },
      { source: 'b.png', name: 'b.png', mime_type: 'image/png', content_base64: btoa('other bytes') },
    ], warnings: [] })) };
    post.mockResolvedValueOnce({ data: { contract_version: 'workflow.v1', request_id: 'fixture', ok: true, result: { resource_id: 'good' } } }).mockRejectedValueOnce(new Error('offline'));
    const result = await content(await prepareMarkdownUpload(input('![a](a.png) ![b](b.png)')));
    expect(manifest(result).images).toEqual([{ source: 'a.png', resource_id: 'good' }]);
    expect(manifest(result).warnings.join()).toContain('b.png');
  });
  it('degrades a host timeout without waiting indefinitely or making image imports', async () => {
    vi.useFakeTimers();
    window.lazymindDesktop = { collectMarkdownImages: () => new Promise(() => {}) };
    const pending = prepareMarkdownUpload(input('![a](a.png)'));
    await vi.advanceTimersByTimeAsync(21000);
    const file = await pending;
    vi.useRealTimers();
    expect(manifest(await content(file)).warnings.join()).toContain('超时');
    expect(post).not.toHaveBeenCalled();
  });
  it('keeps a plain-browser document usable and does not touch the host for remote-only images', async () => {
    const result = await content(await prepareMarkdownUpload(input('# body\n![x](missing.png)')));
    expect(result).toContain('# body');
    expect(manifest(result).images).toEqual([]);
    const file = input('![x](https://example.test/a.png)');
    expect(await prepareMarkdownUpload(file)).toBe(file);
  });
});

for (const count of [3, 10, 32]) {
  it(`imports ${count} images beyond the old fixed deadline`, async () => {
    vi.stubGlobal('crypto', webcrypto);
    let now = 0;
    vi.spyOn(Date, 'now').mockImplementation(() => now);
    const images = Array.from({ length: count }, (_, index) => ({ source: `${index}.png`, name: `${index}.png`,
      mime_type: 'image/png', content_base64: btoa(`image ${index}`) }));
    window.lazymindDesktop = { collectMarkdownImages: async () => { now += count * 2000; return { images, warnings: [] }; } };
    post.mockImplementation(async (_url, body, options) => {
      expect(options.timeout).toBeGreaterThan(0);
      expect(options.timeout).toBeLessThanOrEqual(5000);
      now += 4000;
      return { data: { result: { resource_id: `image-${body.name.replace('.png', '')}` } } };
    });
    const text = images.map(image => `![figure](${image.source})`).join('\n');
    const result = manifest(await content(await prepareMarkdownUpload(input(text))));
    // The overall three-minute cap retains successful imports for unusually slow batches.
    const expected = count === 32 ? 29 : count;
    expect(result.images).toHaveLength(expected);
    expect(result.warnings.length).toBe(count === 32 ? 1 : 0);
  });
}
