import { captureHtmlSlidePng } from './exportHtmlToPptx';
import { beforeEach, describe, it, expect, vi } from 'vitest';
import { embedSlideAssetsForExport, resolveSlideAssets } from './slideAssets';
import { resolveMarkdownImageUrlAsync } from '@/modules/knowledge/utils/imageUrl';
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({
  resolveMarkdownImageUrlAsync: vi.fn(async (path: string) => `https://local.test/api/core${path}?expires=999&sig=fresh`),
}));
beforeEach(() => vi.mocked(resolveMarkdownImageUrlAsync).mockClear());
describe('slide assets', () => {
  it('resolves repeated CSS and img references once without embedding image bytes', async () => {
    const html = '<style>#bg{background:url("/static-files/a.png")}</style><img src="/static-files/a.png">';
    const result = await resolveSlideAssets(html);
    expect(resolveMarkdownImageUrlAsync).toHaveBeenCalledTimes(1);
    expect(result.match(/sig=fresh/g)).toHaveLength(2);
    expect(result).not.toContain('base64');
  });
  it('preserves remote URLs, existing data images and offline relative assets', async () => {
    const html = '<img src="https://other.test/static-files/a.png"><img src="data:image/png;base64,AA"><img src="../images/a.png">';
    expect(await resolveSlideAssets(html)).toBe(html);
  });
});


describe('offline editable export assets', () => {
  const fontPath = `/static-files/presentation-fonts/${'a'.repeat(64)}.ttf`;
  it('keeps verified font references for server embedding without downloading font bytes', async () => {
    const fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);
    try {
      const html = `<style>@font-face{font-family:Test;src:url(${fontPath})}</style>`;
      expect(await embedSlideAssetsForExport(html, { embedFonts: false })).toBe(html);
      expect(fetcher).not.toHaveBeenCalled();
    } finally { vi.unstubAllGlobals(); }
  });
  it('embeds managed font resources for raster capture and signs them for ordinary preview', async () => {
    const fetcher = vi.fn(async () => ({ ok: true, blob: async () => new Blob(['font-bytes'], { type: 'font/ttf' }) }));
    vi.stubGlobal('fetch', fetcher);
    try {
      const html = `<style>@font-face{font-family:Test;src:url(${fontPath})}</style>`;
      expect(await resolveSlideAssets(html)).toContain('sig=fresh');
      expect(await embedSlideAssetsForExport(html)).toContain('data:font/ttf;base64,');
      expect(fetcher).toHaveBeenCalledTimes(1);
    } finally { vi.unstubAllGlobals(); }
  });
  it('embeds both background and foreground images, downloading each once', async () => {
    const fetcher = vi.fn(async () => ({ ok: true, blob: async () => new Blob(['image-bytes'], { type: 'image/png' }) }));
    vi.stubGlobal('fetch', fetcher);
    try {
      const html = '<style>#bg{background:url("/static-files/a.png")}</style><img src="/static-files/a.png">';
      const result = await embedSlideAssetsForExport(html);
      expect(result.match(/data:image\/png;base64,/g)).toHaveLength(2);
      expect(result).not.toContain('/static-files/');
      expect(fetcher).toHaveBeenCalledTimes(1);
    } finally { vi.unstubAllGlobals(); }
  });
  it('fails explicitly on missing images instead of exporting blank slides', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 404 })));
    try {
      await expect(embedSlideAssetsForExport('<img src="/static-files/missing.png">')).rejects.toThrow('404');
    } finally { vi.unstubAllGlobals(); }
  });
});


it('rejects thumbnail capture when an image cannot be embedded instead of caching a blank PNG', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 404 })));
  try {
    await expect(captureHtmlSlidePng('<style>#bg{background:url("/static-files/missing.png")}</style>'))
      .rejects.toThrow('Slide image download failed (404)');
  } finally { vi.unstubAllGlobals(); }
});
