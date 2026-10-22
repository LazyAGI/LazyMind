import { afterEach, expect, it, vi } from 'vitest';
import { downloadWorkflowFile } from './workflowDownload';

const hostOrigin = 'http://127.0.0.1:54321';
const bridgeId = '01234567-89ab-cdef-0123-456789abcdef';
const originalParent = window.parent;
const originalURL = window.location.href;
const parent = { postMessage: vi.fn() };

function embedded() {
  Object.defineProperty(window, 'parent', { configurable: true, value: parent });
  history.replaceState({}, '', `/workflow-runs/test/embed?downloadHostOrigin=${encodeURIComponent(hostOrigin)}&downloadBridgeId=${bridgeId}`);
}
function reply(request: Record<string, unknown>, overrides: Record<string, unknown> = {}, origin = hostOrigin, source: unknown = parent) {
  window.dispatchEvent(new MessageEvent('message', { origin, source: source as Window,
    data: { type: 'lazymind.workflow.download.result', bridgeId, requestId: request.requestId, ok: true, ...overrides } }));
}
afterEach(() => {
  Object.defineProperty(window, 'parent', { configurable: true, value: originalParent });
  history.replaceState({}, '', originalURL);
  parent.postMessage.mockReset();
  vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers();
});

it('retains native link downloads without a host and does not send file data to a parent', async () => {
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  await downloadWorkflowFile({ filename: 'test.md', href: '/file.md' });
  expect(click).toHaveBeenCalledOnce();
  expect(parent.postMessage).not.toHaveBeenCalled();
});

it('sends the exact Blob to the configured parent and waits for the matching save acknowledgement', async () => {
  embedded();
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  const blob = new Blob(['unsaved draft'], { type: 'text/markdown' });
  parent.postMessage.mockImplementation(request => reply(request));
  await downloadWorkflowFile({ filename: 'draft.md', blob });
  expect(parent.postMessage).toHaveBeenCalledTimes(2);
  expect(parent.postMessage.mock.calls[1]).toEqual([
    expect.objectContaining({ action: 'save', filename: 'draft.md', blob }), hostOrigin,
  ]);
  expect(click).not.toHaveBeenCalled();
});

it('ignores forged source, origin, bridge ID and request ID replies', async () => {
  vi.useFakeTimers(); embedded();
  parent.postMessage.mockImplementation(request => {
    reply(request, {}, 'https://evil.example');
    reply(request, {}, hostOrigin, window);
    reply(request, { bridgeId: 'wrong' });
    reply(request, { requestId: 'wrong' });
  });
  const failure = expect(downloadWorkflowFile({ filename: 'draft.md', blob: new Blob(['draft']) })).rejects.toThrow('timed out');
  await vi.advanceTimersByTimeAsync(5001);
  await failure;
  expect(parent.postMessage).toHaveBeenCalledOnce(); // Never sent the content.
});

it('reports cancellation without falling back to a second, blocked browser download', async () => {
  embedded();
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  parent.postMessage.mockImplementation(request => reply(request,
    request.action === 'save' ? { ok: false, error: 'Cancelled' } : {}));
  await expect(downloadWorkflowFile({ filename: 'draft.md', blob: new Blob(['draft']) })).rejects.toThrow('Cancelled');
  expect(click).not.toHaveBeenCalled();
});

it('rejects an untrusted host URL before disclosing file contents', async () => {
  embedded();
  history.replaceState({}, '', `/workflow-runs/test/embed?downloadHostOrigin=https://evil.example&downloadBridgeId=${bridgeId}`);
  await expect(downloadWorkflowFile({ filename: 'draft.md', blob: new Blob(['draft']) })).rejects.toThrow('invalid');
  expect(parent.postMessage).not.toHaveBeenCalled();
});
