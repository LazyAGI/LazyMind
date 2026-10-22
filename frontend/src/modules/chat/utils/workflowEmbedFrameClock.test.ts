import { afterEach, expect, it, vi } from 'vitest';
import { installScratchFrameClock, installWorkflowEmbedFrameClock } from './workflowEmbedFrameClock';

let stop = () => {};
const originalURL = window.location.href;
afterEach(() => {
  stop(); document.body.replaceChildren();
  history.replaceState({}, '', originalURL);
  vi.restoreAllMocks();
});

function scratch(left = -10000, srcdoc = false) {
  const frame = document.createElement('iframe');
  frame.setAttribute('sandbox', 'allow-same-origin');
  // Keep the frame ineligible until its synthetic native methods are installed.
  frame.setAttribute('srcdoc', '<p>preview</p>');
  document.body.appendChild(frame);
  vi.spyOn(frame, 'getBoundingClientRect').mockReturnValue({ left, right: left + 1600,
    top: 0, bottom: 900, width: 1600, height: 900, x: left, y: 0, toJSON() {} });
  const target = frame.contentWindow!;
  const native = vi.fn(() => 12);
  target.requestAnimationFrame = native;
  target.cancelAnimationFrame = vi.fn();
  if (!srcdoc) frame.removeAttribute('srcdoc');
  return { frame, target, native };
}

it('uses the visible page clock only for offscreen scratch frames and cancels correctly', () => {
  const callbacks = new Map<number, FrameRequestCallback>();
  let sequence = 0;
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation(fn => {
    callbacks.set(++sequence, fn); return sequence;
  });
  const cancel = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(id => { callbacks.delete(id); });
  const { target, native } = scratch();
  vi.spyOn(target.performance, 'now').mockReturnValue(123);
  stop = installScratchFrameClock(document);
  const callback = vi.fn();
  const id = target.requestAnimationFrame(callback);
  target.cancelAnimationFrame(id);
  expect(cancel).toHaveBeenCalledWith(1);
  expect(callbacks.size).toBe(0);
  target.requestAnimationFrame(callback);
  callbacks.get(2)!(999);
  expect(callback).toHaveBeenCalledOnce();
  expect(callback).toHaveBeenCalledWith(123);
  expect(native).not.toHaveBeenCalled();
  stop();
  expect(target.requestAnimationFrame).toBe(native);
});

it('retains native scheduling for visible scratch frames and leaves srcdoc previews untouched', () => {
  const visible = scratch(0);
  const preview = scratch(-10000, true);
  stop = installScratchFrameClock(document);
  visible.target.requestAnimationFrame(() => {});
  expect(visible.native).toHaveBeenCalledOnce();
  expect(preview.target.requestAnimationFrame).toBe(preview.native);
});

it('patches dynamically inserted scratch frames before their asynchronous load continuation', async () => {
  const parent = vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(22);
  stop = installScratchFrameClock(document);
  const { frame, target, native } = scratch();
  const continuation = vi.fn(() => { frame.contentWindow!.requestAnimationFrame(() => {}); });
  frame.addEventListener('load', continuation);
  await Promise.resolve(); // Browser frame loads follow the DOM mutation microtask.
  expect(target.requestAnimationFrame).not.toBe(native);
  frame.dispatchEvent(new Event('load'));
  expect(continuation).toHaveBeenCalledOnce();
  expect(parent).toHaveBeenCalledOnce();
  expect(native).not.toHaveBeenCalled();
});

it('restores removed frames and cancels pending callbacks on pagehide', async () => {
  vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(22);
  const cancel = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
  const { frame, target, native } = scratch();
  stop = installScratchFrameClock(document);
  target.requestAnimationFrame(() => {});
  frame.remove();
  await Promise.resolve();
  expect(cancel).toHaveBeenCalledWith(22);
  expect(target.requestAnimationFrame).toBe(native);
  window.dispatchEvent(new Event('pagehide'));
});

it('does nothing without explicit host opt-in or in a top-level window', () => {
  const { target, native } = scratch();
  stop = installWorkflowEmbedFrameClock();
  expect(target.requestAnimationFrame).toBe(native);
  history.replaceState({}, '', '/workflow-runs/test/embed?workbuddyFrameClock=1');
  stop = installWorkflowEmbedFrameClock();
  expect(target.requestAnimationFrame).toBe(native);
});
