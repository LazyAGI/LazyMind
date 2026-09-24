import { beforeEach, afterEach, expect, it, vi } from 'vitest';
const capture = vi.hoisted(() => vi.fn());
vi.mock('./exportHtmlToPptx', () => ({ captureHtmlSlidePng: capture }));
let disk: Map<string, Response>;
beforeEach(() => {
  vi.resetModules();
  capture.mockReset().mockResolvedValue('data:image/png;base64,YQ==');
  disk = new Map();
  vi.stubGlobal('caches', { open: vi.fn(async () => ({
    match: async (key: string) => disk.get(key)?.clone(),
    put: async (key: string, response: Response) => { disk.set(key, response); },
    keys: async () => [...disk.keys()].map(url => new Request(url)),
    delete: async (key: string | Request) => disk.delete(typeof key === 'string' ? key : key.url),
  })) });
});
afterEach(() => vi.unstubAllGlobals());

it('loads a stored thumbnail after a page reload without another screenshot', async () => {
  const first = await import('./slideRasterCache');
  const key = first.rasterCacheKey('session', 'page-r1');
  await first.ensureRasterPng(key, '<html>one</html>');
  vi.resetModules(); // Fresh JS memory, same browser disk cache.
  const second = await import('./slideRasterCache');
  expect(second.getRasterPng(key)).toBeNull();
  expect(await second.loadRasterPng(key)).toBe('data:image/png;base64,YQ==');
  expect(await second.ensureRasterPng(key, '<html>one</html>')).toBe('data:image/png;base64,YQ==');
  expect(capture).toHaveBeenCalledTimes(1);
});

it('deduplicates concurrent misses and captures a new revision independently', async () => {
  const store = await import('./slideRasterCache');
  await Promise.all([store.ensureRasterPng('r1', 'one'), store.ensureRasterPng('r1', 'one')]);
  await store.ensureRasterPng('r2', 'edited');
  expect(capture).toHaveBeenCalledTimes(2);
});

it('replaces stored images when forced and does not reuse insufficient resolution', async () => {
  const store = await import('./slideRasterCache');
  await store.setRasterPng('page', 'data:image/png;base64,Yg==', 1);
  await store.ensureRasterPng('page', 'html', { pixelRatio: 2 });
  await store.ensureRasterPng('page', 'html', { force: true });
  expect(capture).toHaveBeenCalledTimes(2);
});

it('still renders when browser storage is disabled, and never saves failed captures', async () => {
  vi.stubGlobal('caches', { open: vi.fn().mockRejectedValue(new Error('unavailable')) });
  const store = await import('./slideRasterCache');
  expect(await store.ensureRasterPng('good', 'html')).toContain('data:image/png');
  capture.mockRejectedValueOnce(new Error('missing image'));
  await expect(store.ensureRasterPng('bad', 'html')).rejects.toThrow('missing image');
  expect(store.getRasterPng('bad')).toBeNull();
});

it('invalidates only the requested workflow session on disk', async () => {
  const store = await import('./slideRasterCache');
  await store.ensureRasterPng(store.rasterCacheKey('a', 'page'), 'a');
  await store.ensureRasterPng(store.rasterCacheKey('b', 'page'), 'b');
  await store.invalidateRasterSession('a');
  vi.resetModules();
  const fresh = await import('./slideRasterCache');
  expect(await fresh.loadRasterPng(fresh.rasterCacheKey('a', 'page'))).toBeNull();
  expect(await fresh.loadRasterPng(fresh.rasterCacheKey('b', 'page'))).toContain('data:image/png');
});
