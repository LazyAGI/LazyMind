import { captureHtmlSlidePng } from './exportHtmlToPptx';
import { describe, it, expect, vi } from 'vitest';
import { embedSlideAssetsForExport, resolveSlideAssets } from './slideAssets';
import { resolveMarkdownImageUrlAsync } from '@/modules/knowledge/utils/imageUrl';
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({
  resolveMarkdownImageUrlAsync: vi.fn(async (path: string) => `https://local.test/api/core${path}?expires=999&sig=fresh`),
}));
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
