import { webcrypto } from 'node:crypto';
import { beforeEach, afterEach, expect, it, vi } from 'vitest';
import { clearWriterDownloadMemoryCache, prepareWriterDownloadBlob } from './WriterDownloadFormat';

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock('@/components/request', () => ({ axiosInstance: api }));
vi.mock('@/runtime/apiBase', () => ({ coreApiUrl: (path: string) => path }));

beforeEach(() => {
  vi.resetAllMocks();
  clearWriterDownloadMemoryCache();
  vi.stubGlobal('crypto', webcrypto);
  api.get.mockRejectedValue({ response: { status: 404 } });
  api.put.mockResolvedValue({});
});
afterEach(() => vi.unstubAllGlobals());

it('exports LaTeX with automatic language selection and reuses the current conversion', async () => {
  const blob = new Blob(['English LaTeX']);
  api.post.mockResolvedValue({ data: blob });
  const source = {
    filename: 'paper.tex', cacheKey: 'english-paper', conversionSourceFormat: 'markdown' as const,
    conversionSource: '# English Paper\n\n## Abstract\n\nA concise abstract.',
  };
  expect(await prepareWriterDownloadBlob(source, 'latex')).toBe(blob);
  expect(api.post.mock.calls[0][1]).toEqual({
    source_format: 'markdown', target_format: 'latex', content: source.conversionSource,
    document_id: 'paper', materialized_numbering: true,
  });
  const legacyBytes = new TextEncoder().encode(
    `lazyllm-writer-conversion-v1\0markdown:true\0${source.conversionSource}`,
  );
  const legacyHash = Buffer.from(await webcrypto.subtle.digest('SHA-256', legacyBytes)).toString('hex');
  expect(api.get.mock.calls[0][0]).not.toContain(legacyHash);
  expect(await prepareWriterDownloadBlob(source, 'latex')).toBe(blob);
  expect(api.post).toHaveBeenCalledTimes(1);
});
