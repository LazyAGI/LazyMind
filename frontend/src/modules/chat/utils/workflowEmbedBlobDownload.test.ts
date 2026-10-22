import { afterEach, expect, it, vi } from 'vitest';
// Select the browser build; Vitest otherwise resolves jsPDF's filesystem-saving Node build.
// @ts-expect-error jsPDF does not publish a declaration for this browser subpath.
import { jsPDF } from 'jspdf/dist/jspdf.es.js';
import { installBlobDownloadAdapter, installWorkflowEmbedBlobDownloads } from './workflowEmbedBlobDownload';

let stop = () => {};
afterEach(() => { stop(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });
function anchor() {
  const link = document.createElement('a');
  link.download = 'generated.pdf';
  link.href = `blob:${window.location.origin}/generated`;
  return link;
}

it('delivers the exact PDF Blob and filename from the real jsPDF detached, non-cancelable click', async () => {
  vi.useFakeTimers();
  let generated: Blob | undefined;
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL(blob: Blob | MediaSource) {
      generated = blob as Blob; return `blob:${window.location.origin}/generated`;
    }
    static revokeObjectURL() {}
  });
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => generated })));
  const deliver = vi.fn(async () => {});
  const report = vi.fn();
  stop = installBlobDownloadAdapter(deliver, report);
  const pdf = new jsPDF();
  pdf.text('Host download compatibility probe', 10, 10);
  pdf.save('unchanged-export.pdf');
  await vi.advanceTimersByTimeAsync(1);
  expect(generated?.type).toBe('application/pdf');
  expect(generated!.size).toBeGreaterThan(1000);
  expect(deliver).toHaveBeenCalledOnce();
  expect(deliver).toHaveBeenCalledWith({ filename: 'unchanged-export.pdf', blob: generated });
  expect(report).toHaveBeenCalledWith('blob-download-returned', undefined);
});

it('starts reading before returning and coalesces duplicate activation while delivery is pending', async () => {
  const blob = new Blob(['original bytes']);
  const fetch = vi.fn(async () => ({ ok: true, blob: async () => blob }));
  vi.stubGlobal('fetch', fetch);
  let finish!: () => void;
  const deliver = vi.fn(() => new Promise<void>(resolve => { finish = resolve; }));
  stop = installBlobDownloadAdapter(deliver, vi.fn());
  const link = anchor();
  link.click();
  expect(fetch).toHaveBeenCalledOnce();
  link.dispatchEvent(new MouseEvent('click'));
  expect(fetch).toHaveBeenCalledOnce();
  await vi.waitFor(() => expect(deliver).toHaveBeenCalledOnce());
  expect(deliver).toHaveBeenCalledWith({ filename: 'generated.pdf', blob });
  finish();
});

it('reports host errors without falling back to another browser download', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, blob: async () => new Blob(['x']) })));
  const report = vi.fn();
  stop = installBlobDownloadAdapter(async () => { throw Error('Cancelled'); }, report);
  anchor().dispatchEvent(new MouseEvent('click'));
  await vi.waitFor(() => expect(report).toHaveBeenCalledWith('blob-download-error', 'Cancelled'));
});

it('leaves ordinary and already-cancelled link events on the native path', () => {
  const deliver = vi.fn();
  stop = installBlobDownloadAdapter(deliver, vi.fn());
  const link = document.createElement('a');
  link.href = '/ordinary-file';
  link.download = 'ordinary-file';
  const clicked = vi.fn((event: Event) => event.preventDefault());
  link.addEventListener('click', clicked);
  link.dispatchEvent(new MouseEvent('click', { cancelable: true }));
  const cancelled = new MouseEvent('click', { cancelable: true });
  cancelled.preventDefault();
  anchor().dispatchEvent(cancelled);
  expect(clicked).toHaveBeenCalledOnce();
  expect(deliver).not.toHaveBeenCalled();
});

it('restores inherited browser methods and suppresses late delivery on pagehide', async () => {
  const original = HTMLAnchorElement.prototype.dispatchEvent;
  let finish!: (response: object) => void;
  vi.stubGlobal('fetch', vi.fn(() => new Promise(resolve => { finish = resolve; })));
  const deliver = vi.fn();
  stop = installBlobDownloadAdapter(deliver, vi.fn());
  anchor().click();
  window.dispatchEvent(new Event('pagehide'));
  expect(HTMLAnchorElement.prototype.dispatchEvent).toBe(original);
  finish({ ok: true, blob: async () => new Blob(['x']) });
  await Promise.resolve(); await Promise.resolve();
  expect(deliver).not.toHaveBeenCalled();
});

it('does not install globally without the explicit WorkBuddy opt-in', () => {
  const original = HTMLAnchorElement.prototype.dispatchEvent;
  stop = installWorkflowEmbedBlobDownloads();
  expect(HTMLAnchorElement.prototype.dispatchEvent).toBe(original);
});
