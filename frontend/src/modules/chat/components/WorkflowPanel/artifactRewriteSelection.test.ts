import { afterEach, describe, expect, it } from 'vitest';
import { selectedMarkdownParagraph } from './artifactRewriteSelection';

const selectionRect = {
  bottom: 60,
  height: 20,
  left: 20,
  right: 180,
  top: 40,
  width: 160,
  x: 20,
  y: 40,
  toJSON: () => ({}),
};

function selectRange(range: Range): void {
  Object.defineProperty(range, 'getBoundingClientRect', {
    configurable: true,
    value: () => selectionRect,
  });
  const selection = window.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function paragraphFixture(): {
  container: HTMLElement;
  editable: HTMLElement;
  first: HTMLParagraphElement;
  second: HTMLParagraphElement;
} {
  const container = document.createElement('section');
  container.innerHTML = [
    '<div contenteditable="true">',
    '<p><span>Alpha beta</span></p>',
    '<p><span>Gamma</span></p>',
    '</div>',
  ].join('');
  document.body.append(container);
  const editable = container.firstElementChild as HTMLElement;
  const [first, second] = editable.querySelectorAll('p');
  return { container, editable, first, second };
}

afterEach(() => {
  window.getSelection()?.removeAllRanges();
  document.body.innerHTML = '';
});

describe('selectedMarkdownParagraph', () => {
  it('accepts a whole paragraph when the browser places both endpoints on its parent', () => {
    const { container, editable, first } = paragraphFixture();
    const range = document.createRange();
    range.setStart(editable, 0);
    range.setEnd(editable, 1);
    selectRange(range);

    const selected = selectedMarkdownParagraph(container);

    expect(selected).toMatchObject({
      text: 'Alpha beta',
      supported: true,
      paragraph: first,
      startOffset: 0,
    });
  });

  it('accepts a partial paragraph ending at the paragraph boundary', () => {
    const { container, editable, first } = paragraphFixture();
    const text = first.querySelector('span')!.firstChild!;
    const range = document.createRange();
    range.setStart(text, 6);
    range.setEnd(editable, 1);
    selectRange(range);

    const selected = selectedMarkdownParagraph(container);

    expect(selected).toMatchObject({
      text: 'beta',
      supported: true,
      paragraph: first,
      startOffset: 6,
    });
  });

  it('keeps a real multi-paragraph selection unsupported', () => {
    const { container, editable } = paragraphFixture();
    const range = document.createRange();
    range.setStart(editable, 0);
    range.setEnd(editable, 2);
    selectRange(range);

    expect(selectedMarkdownParagraph(container)?.supported).toBe(false);
  });
});
