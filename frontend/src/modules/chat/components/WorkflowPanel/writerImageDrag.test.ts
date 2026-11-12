import { fireEvent } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { startWriterImageDrag } from './writerImageDrag';

let cancel: (() => void) | undefined;
let nextFrame: FrameRequestCallback;
const originalHitTest = Object.getOwnPropertyDescriptor(document, 'elementFromPoint');
beforeEach(() => {
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => { nextFrame = callback; return 1; });
  vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
});
afterEach(() => {
  cancel?.(); document.body.replaceChildren(); vi.restoreAllMocks();
  if (originalHitTest) Object.defineProperty(document, 'elementFromPoint', originalHitTest);
  else Reflect.deleteProperty(document, 'elementFromPoint');
});

function setup() {
  const root = document.createElement('section');
  root.className = 'writer-image-group'; root.dataset.layout = 'sliding';
  root.innerHTML = '<div class="writer-image-group-images" style="overflow-x:auto"><img alt="Source"><img alt="Target"></div>';
  document.body.append(root);
  const rail = root.firstElementChild as HTMLElement, image = rail.children[0] as HTMLImageElement;
  const target = rail.children[1] as HTMLImageElement;
  vi.spyOn(image, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 100, 100));
  vi.spyOn(target, 'getBoundingClientRect').mockReturnValue(new DOMRect(100, 0, 100, 100));
  vi.spyOn(rail, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 200, 100));
  Object.defineProperties(rail, { clientWidth: { value: 200 }, scrollWidth: { value: 600 } });
  Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: () => target });
  const onDrop = vi.fn();
  let valid = true;
  cancel = startWriterImageDrag(new MouseEvent('mousedown', { clientX: 20, clientY: 20 }), image, root,
    () => valid ? { element: target, position: 'after' } : null, onDrop);
  return { image, rail, onDrop, outside: () => { valid = false; } };
}

it('tracks the pointer and scrolls the gallery without committing until release', () => {
  const { image, rail, onDrop } = setup();
  fireEvent.mouseMove(window, { clientX: 22, clientY: 20 });
  expect(document.querySelector('.writer-image-drag-preview')).toBeNull();
  fireEvent.mouseMove(window, { clientX: 195, clientY: 50 });
  const preview = document.querySelector<HTMLElement>('.writer-image-drag-preview')!;
  expect(preview.style.transform).toBe('translate3d(175px, 30px, 0)');
  expect(image).toHaveClass('writer-image-drag-source');
  expect(rail.scrollLeft).toBeGreaterThan(0);
  expect(document.querySelector<HTMLElement>('.writer-image-drag-indicator')!.style.left).toBe('200px');
  fireEvent.mouseMove(window, { clientX: 160, clientY: 60 }); nextFrame(16);
  expect(preview.style.transform).toBe('translate3d(140px, 40px, 0)');
  expect(onDrop).not.toHaveBeenCalled();
  fireEvent.mouseUp(window, { clientX: 160, clientY: 60 });
  expect(onDrop).toHaveBeenCalledOnce();
  expect(preview).not.toBeInTheDocument();
  expect(image).not.toHaveClass('writer-image-drag-source');
});

it.each(['Escape', 'blur', 'outside', 'unmount'])('cancels cleanly on %s', reason => {
  const { image, onDrop, outside } = setup();
  fireEvent.mouseMove(window, { clientX: 180, clientY: 50 });
  if (reason === 'Escape') fireEvent.keyDown(window, { key: 'Escape' });
  if (reason === 'blur') fireEvent.blur(window);
  if (reason === 'outside') outside();
  if (reason === 'unmount') cancel?.();
  fireEvent.mouseUp(window, { clientX: 180, clientY: 50 });
  expect(onDrop).not.toHaveBeenCalled();
  expect(image).not.toHaveClass('writer-image-drag-source');
  expect(document.querySelector('.writer-image-drag-preview')).toBeNull();
  expect(document.querySelector('.writer-image-drag-indicator')).toBeNull();
});
