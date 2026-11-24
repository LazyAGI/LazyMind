import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { createRef, useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import {
  createRootEditorSubscription$, headingsPlugin, imagePlugin, linkPlugin, MDXEditor,
  realmPlugin, type MDXEditorMethods,
} from '@mdxeditor/editor';
import { REDO_COMMAND, UNDO_COMMAND, type LexicalEditor } from 'lexical';
import i18n from '@/i18n';
import { WriterImageGroupContext, writerImageGroupPlugin } from './writerImageGroupPlugin';
import type { WriterImageGroup } from './writerImageGroups';

const originalHitTest = Object.getOwnPropertyDescriptor(document, 'elementFromPoint');
afterEach(() => {
  fireEvent.keyDown(window, { key: 'Escape' }); vi.restoreAllMocks(); vi.unstubAllGlobals();
  if (originalHitTest) Object.defineProperty(document, 'elementFromPoint', originalHitTest);
  else Reflect.deleteProperty(document, 'elementFromPoint');
});

it.each([null, '', 'Original text '])('reorders images, retaining links, dimensions and undo history (%s)', async prefix => {
  await i18n.changeLanguage('en-US');
  vi.stubGlobal('Image', function () {
    const image = document.createElement('img');
    Object.defineProperty(image, 'src', {
      set(value: string) { image.setAttribute('src', value); queueMicrotask(() => image.dispatchEvent(new Event('load'))); },
      get() { return image.getAttribute('src') ?? ''; },
    });
    return image;
  });
  let editor!: LexicalEditor;
  const captureEditor = realmPlugin({ init(realm) {
    realm.pub(createRootEditorSubscription$, value => { editor = value; return () => {}; });
  } });
  const methods = createRef<MDXEditorMethods>();
  const single = '![Single](https://example.com/c.png)';
  const link = '(https://example.com/article "Article")';
  const original = prefix === null ? single : `[${prefix}${single}]${link}`;
  const moved = prefix === null ? single : `[${single}]${link}`;
  const source = `# Heading\n\n![A](https://example.com/a.png)\n<img src="https://example.com/b.png" width="96" alt="B" />\n\nBody\n\nNext paragraph\n\n${original}`;
  const initialGroups: WriterImageGroup[] = [{ layout: 'sliding', images: [
    { source: 'https://example.com/a.png', occurrence: 0 }, { source: 'https://example.com/b.png', occurrence: 0 },
  ] }];
  let savedGroups = initialGroups;
  function Editor() {
    const [groups, setGroups] = useState(initialGroups);
    return <WriterImageGroupContext.Provider value={{ groups, onChange: value => { savedGroups = value; setGroups(value); } }}>
    <div data-testid='viewport' style={{ overflowY: 'auto' }}>
      <MDXEditor ref={methods} markdown={source} suppressHtmlProcessing plugins={[headingsPlugin(), imagePlugin(), linkPlugin(), writerImageGroupPlugin(), captureEditor()]} />
    </div>
    </WriterImageGroupContext.Provider>;
  }
  const view = render(<Editor />);
  await waitFor(() => expect(view.container.querySelectorAll('img')).toHaveLength(3));
  const viewport = view.getByTestId('viewport');
  vi.spyOn(viewport, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 400, 240));
  Object.defineProperties(viewport, { clientHeight: { value: 240 }, scrollHeight: { value: 1200 } });
  const images = () => Array.from(view.container.querySelectorAll('img')).map(image => image.alt);
  const drag = (image: HTMLImageElement, target: HTMLElement, x: number, y: number) => {
    Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: () => target });
    vi.spyOn(image, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 80, 80));
    fireEvent.mouseDown(image, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.mouseMove(window, { clientX: x, clientY: y });
  };
  const before = methods.current!.getMarkdown();
  const second = view.getByAltText('B');
  vi.spyOn(second, 'getBoundingClientRect').mockReturnValue(new DOMRect(100, 0, 80, 80));
  const rail = second.closest<HTMLElement>('.writer-image-group-images')!;
  rail.style.overflowX = 'auto';
  vi.spyOn(rail, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 200, 100));
  Object.defineProperties(rail, { clientWidth: { value: 200 }, scrollWidth: { value: 600 } });
  drag(view.getByAltText('A') as HTMLImageElement, rail, 190, 40);
  expect(methods.current!.getMarkdown()).toBe(before);
  expect(rail.scrollLeft).toBeGreaterThan(0);
  await act(async () => fireEvent.mouseUp(window, { clientX: 190, clientY: 40 }));
  await waitFor(() => expect(images()).toEqual(['B', 'A', 'Single']));
  expect(view.getByAltText('B')).toHaveAttribute('width', '96');
  expect(view.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding');
  await act(async () => { editor.dispatchCommand(UNDO_COMMAND, undefined); });
  await waitFor(() => expect(images()).toEqual(['A', 'B', 'Single']));
  await act(async () => { editor.dispatchCommand(REDO_COMMAND, undefined); });
  await waitFor(() => expect(images()).toEqual(['B', 'A', 'Single']));

  const heading = view.getByRole('heading');
  vi.spyOn(heading, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 100, 300, 30));
  drag(view.getByAltText('Single') as HTMLImageElement, heading.parentElement!, 100, 95);
  await act(async () => fireEvent.mouseUp(window, { clientX: 100, clientY: 95 }));
  await waitFor(() => expect(methods.current!.getMarkdown().startsWith(moved)).toBe(true));
  if (prefix) expect(view.getByRole('link', { name: prefix.trim() })).toHaveAttribute('href', 'https://example.com/article');
  expect(methods.current!.getMarkdown()).not.toContain(`[]${link}`);
  expect(view.getByText('Body')).toBeInTheDocument();
  expect(view.getByAltText('B')).toHaveAttribute('width', '96');
  await act(async () => { editor.dispatchCommand(UNDO_COMMAND, undefined); });
  await waitFor(() => expect(images()).toEqual(['B', 'A', 'Single']));

  vi.spyOn(view.getByText('Body').closest('p')!, 'getBoundingClientRect').mockReturnValue(new DOMRect(22, 180, 300, 40));
  vi.spyOn(view.getByText('Next paragraph').closest('p')!, 'getBoundingClientRect').mockReturnValue(new DOMRect(22, 270, 300, 40));
  const beforeMoveGroups = savedGroups;
  drag(view.getByAltText('A') as HTMLImageElement, editor.getRootElement()!, 100, 245);
  await act(async () => fireEvent.mouseUp(window, { clientX: 100, clientY: 245 }));
  await waitFor(() => expect(methods.current!.getMarkdown()).toContain('Body\n\n![A](https://example.com/a.png)\n\nNext paragraph'));
  expect(savedGroups).toEqual([]);
  await act(async () => { editor.dispatchCommand(UNDO_COMMAND, undefined); });
  await waitFor(() => expect(savedGroups).toEqual(beforeMoveGroups));
  expect(view.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding');

  let nextFrame!: FrameRequestCallback;
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => { nextFrame = callback; return 1; });
  const unchanged = methods.current!.getMarkdown();
  viewport.scrollTop = 120;
  drag(view.getByAltText('Single') as HTMLImageElement, document.body, 120, 600);
  const first = viewport.scrollTop;
  expect(first).toBeGreaterThan(120);
  nextFrame(16);
  const lower = viewport.scrollTop;
  expect(lower).toBeGreaterThan(first);
  fireEvent.mouseMove(window, { clientX: 120, clientY: -200 }); nextFrame(32);
  expect(viewport.scrollTop).toBeLessThan(lower);
  await act(async () => fireEvent.mouseUp(window, { clientX: 120, clientY: -200 }));
  expect(methods.current!.getMarkdown()).toBe(unchanged);
  expect(document.querySelector('.writer-image-drag-preview')).toBeNull();
  drag(view.getByAltText('Single') as HTMLImageElement, heading.parentElement!, 100, 95);
  fireEvent.keyDown(window, { key: 'Escape' });
  await act(async () => fireEvent.mouseUp(window, { clientX: 100, clientY: 95 }));
  expect(methods.current!.getMarkdown()).toBe(unchanged);
});
