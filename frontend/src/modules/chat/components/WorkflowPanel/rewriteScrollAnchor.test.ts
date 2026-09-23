import { afterEach, expect, it, vi } from 'vitest';
import { captureRewriteScrollAnchor } from './rewriteScrollAnchor';

afterEach(() => { document.body.innerHTML = ''; vi.unstubAllGlobals(); });

it.each([[-180, 0], [60, 60]])('keeps the rewritten block visible after replacement (old offset %s)', (oldOffset, expectedOffset) => {
  let frame: FrameRequestCallback | undefined;
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { frame = callback; return 1; });
  const scroller = document.createElement('div');
  scroller.style.overflowY = 'auto';
  Object.defineProperties(scroller, { scrollHeight: { value: 2000 }, clientHeight: { value: 400 } });
  scroller.scrollTop = 600;
  scroller.getBoundingClientRect = () => ({ top: 100 } as DOMRect);
  scroller.innerHTML = '<article class="writer-ir__document--editable"><div data-node-id="reviewed"><p data-writer-block-content>Before</p></div></article>';
  document.body.append(scroller);
  const editor = scroller.firstElementChild!;
  const target = editor.querySelector('p')!;
  target.getBoundingClientRect = () => ({ top: 100 + oldOffset } as DOMRect);
  const restore = captureRewriteScrollAnchor([target]);
  // Accept replaces the editable DOM, removing the diff's extra height.
  editor.innerHTML = '<div data-node-id="reviewed"><p data-writer-block-content>Accepted</p></div>';
  const replacement = editor.querySelector('p')!;
  replacement.getBoundingClientRect = () => ({ top: 100 + 300 - scroller.scrollTop } as DOMRect);
  restore();
  expect(scroller.scrollTop).toBe(600);
  frame!(0);
  expect(replacement.getBoundingClientRect().top - 100).toBe(expectedOffset);
});

it('anchors batch acceptance to the reviewed block nearest the viewport', () => {
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { callback(0); return 1; });
  const scroller = document.createElement('div');
  scroller.style.overflowY = 'auto';
  Object.defineProperties(scroller, { scrollHeight: { value: 2000 }, clientHeight: { value: 400 } });
  scroller.getBoundingClientRect = () => ({ top: 0 } as DOMRect);
  scroller.scrollTop = 600;
  scroller.innerHTML = '<p>Earlier</p><p>Visible</p>';
  document.body.append(scroller);
  const [earlier, visible] = Array.from(scroller.children) as HTMLElement[];
  earlier.getBoundingClientRect = () => ({ top: -500 } as DOMRect);
  let visibleTop = 40;
  visible.getBoundingClientRect = () => ({ top: visibleTop } as DOMRect);
  const restore = captureRewriteScrollAnchor([earlier, visible]);
  visibleTop = -160;
  restore();
  expect(scroller.scrollTop).toBe(400);
});
