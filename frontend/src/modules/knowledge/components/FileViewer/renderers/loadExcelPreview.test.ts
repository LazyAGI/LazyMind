import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadExcelPreview } from './loadExcelPreview';

const previewWindow = window as typeof window & { jsPreviewExcel?: unknown };
afterEach(() => {
  delete previewWindow.jsPreviewExcel;
  document.querySelectorAll('script[src="/js-preview-excel.umd.js"]').forEach(script => script.remove());
});

describe('on-demand Excel preview library', () => {
  it('loads once for concurrent previews and reuses the loaded library', async () => {
    expect(document.querySelector('script[src="/js-preview-excel.umd.js"]')).toBeNull();
    const first = loadExcelPreview();
    const second = loadExcelPreview();
    const scripts = document.querySelectorAll('script[src="/js-preview-excel.umd.js"]');
    expect(scripts).toHaveLength(1);
    const library = { init: vi.fn() };
    previewWindow.jsPreviewExcel = library;
    scripts[0].dispatchEvent(new Event('load'));
    expect(await first).toBe(library);
    expect(await second).toBe(library);
    expect(await loadExcelPreview()).toBe(library);
    expect(document.querySelectorAll('script[src="/js-preview-excel.umd.js"]')).toHaveLength(1);
  });

  it('reports a failed load and permits a later preview to retry', async () => {
    const first = loadExcelPreview();
    const failure = expect(first).rejects.toThrow('Failed to load Excel preview library');
    document.querySelector('script[src="/js-preview-excel.umd.js"]')!.dispatchEvent(new Event('error'));
    await failure;
    const retry = loadExcelPreview();
    previewWindow.jsPreviewExcel = { init: vi.fn() };
    document.querySelector('script[src="/js-preview-excel.umd.js"]')!.dispatchEvent(new Event('load'));
    expect(await retry).toBe(previewWindow.jsPreviewExcel);
  });
});
