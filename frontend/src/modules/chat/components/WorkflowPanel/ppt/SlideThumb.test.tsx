import { createRef } from 'react';
import { act, cleanup, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SlideThumb, type SlideThumbHandle } from './SlideThumb';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';

const raster = vi.hoisted(() => vi.fn());
vi.mock('./exportHtmlToPptx', () => ({ extractHtmlFromArtifact: (raw: string) => raw }));
vi.mock('./slideRasterCache', () => ({
  RASTER_EXPORT_PIXEL_RATIO: 2, ensureRasterPng: raster,
  fingerprintArtifact: JSON.stringify, fingerprintText: (s: string) => s,
  rasterCacheKey: (s: string, k: string) => s + k,
  getRasterPng: () => null, loadRasterPng: async () => null, setRasterPng: async () => {},
}));
let observers: { callback: (entries: { isIntersecting: boolean }[]) => void; disconnect: ReturnType<typeof vi.fn> }[];
const slot = { slot_id: 'preview_html', slot: 'preview_html', list_index: 3, revision: 1,
  artifact_value: { text: '<html><body>Page 4</body></html>' } } as SlotRevision;
const image = 'data:image/png;base64,YQ==';
beforeEach(() => {
  raster.mockReset(); observers = [];
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  vi.stubGlobal('IntersectionObserver', class {
    disconnect = vi.fn(); observe = vi.fn();
    constructor(callback: (entries: { isIntersecting: boolean }[]) => void) { observers.push({callback, disconnect: this.disconnect}); }
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
const intersect = (visible: boolean) => act(() => observers[observers.length - 1].callback([{ isIntersecting: visible }]));
const failed = async () => waitFor(() => expect(observers.length).toBeGreaterThan(0));

it('recovers a failed cross-origin hidden thumbnail when it becomes visible', async () => {
  raster.mockRejectedValueOnce(new Error('slide capture timed out')).mockResolvedValue(image);
  const view = render(<SlideThumb slot={slot} sessionId="run" />);
  await failed();
  intersect(false);
  expect(raster).toHaveBeenCalledTimes(1);
  intersect(true);
  await waitFor(() => expect(view.container.querySelector('img')).not.toBeNull());
  expect(raster).toHaveBeenCalledTimes(2);
  expect(console.warn).toHaveBeenCalledWith('[workflow] slide thumbnail failed', expect.objectContaining({listIndex: 3, message: 'slide capture timed out'}));
});

it('bounds automatic retries and still allows keyboard/page-button retry', async () => {
  raster.mockRejectedValue(new Error('offline'));
  const ref = createRef<SlideThumbHandle>();
  const view = render(<button onClick={() => ref.current?.retry()}><SlideThumb ref={ref} slot={slot} sessionId="run" /></button>);
  await failed(); intersect(true);
  await waitFor(() => expect(observers.length).toBe(2)); intersect(true);
  await waitFor(() => expect(raster).toHaveBeenCalledTimes(3));
  await waitFor(() => expect(view.container.querySelector('[title]')).not.toBeNull());
  act(() => window.dispatchEvent(new Event('focus')));
  expect(raster).toHaveBeenCalledTimes(3);
  raster.mockResolvedValue(image);
  act(() => view.getByRole('button').click()); // The parent button also receives keyboard activation.
  await waitFor(() => expect(view.container.querySelector('img')).not.toBeNull());
  expect(raster).toHaveBeenCalledTimes(4);
});

it('does not apply a late capture to a different artifact or after unmount', async () => {
  let finish!: (png: string) => void;
  raster.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; })).mockResolvedValue(image);
  const view = render(<SlideThumb slot={slot} sessionId="run" />);
  await waitFor(() => expect(raster).toHaveBeenCalledTimes(1));
  view.rerender(<SlideThumb slot={{...slot, revision: 2}} sessionId="run" />);
  await waitFor(() => expect(view.container.querySelector('img')).toHaveAttribute('src', image));
  await act(async () => finish('data:image/png;base64,OLD'));
  expect(view.container.querySelector('img')).toHaveAttribute('src', image);
  view.unmount();
  expect(console.warn).not.toHaveBeenCalled();
});
