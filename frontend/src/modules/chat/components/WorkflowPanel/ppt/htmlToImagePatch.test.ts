import { createImage } from 'html-to-image/lib/util';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
let image: { onload: () => void; decode: ReturnType<typeof vi.fn> };
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal('Image', class { onload = () => {}; decode = vi.fn(async () => {}); constructor() { image = this; } });
  vi.stubGlobal('requestAnimationFrame', vi.fn(() => 7));
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
});
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

it('settles decoded images when a cross-origin hidden frame never dispatches rAF', async () => {
  const pending = createImage('data:image/svg+xml,test');
  image.onload();
  await vi.advanceTimersByTimeAsync(100);
  expect(await pending).toBe(image);
  expect(cancelAnimationFrame).toHaveBeenCalledWith(7);
  expect(vi.getTimerCount()).toBe(0);
});

it('keeps the ordinary paint callback and clears its fallback timer', async () => {
  let paint!: FrameRequestCallback;
  vi.mocked(requestAnimationFrame).mockImplementation(callback => { paint = callback; return 7; });
  const pending = createImage('data:image/svg+xml,test'); image.onload();
  await Promise.resolve(); paint(0);
  expect(await pending).toBe(image);
  expect(vi.getTimerCount()).toBe(0);
});

it('rejects image decoding errors rather than hanging until the capture timeout', async () => {
  const pending = createImage('bad');
  const rejected = expect(pending).rejects.toThrow('decode failed');
  image.decode.mockRejectedValue(new Error('decode failed')); image.onload();
  await rejected;
  expect(requestAnimationFrame).not.toHaveBeenCalled();
  expect(vi.getTimerCount()).toBe(0);
});
