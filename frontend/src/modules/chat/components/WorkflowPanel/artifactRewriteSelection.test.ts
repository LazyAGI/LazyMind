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
  it.each(['h1', 'h2', 'h6', 'li'])('accepts selected text inside %s', tag => {
    const container = document.createElement('div');
    container.innerHTML = `<${tag}>😀 <strong>Selected</strong> text</${tag}>`;
    document.body.append(container);
    const range = document.createRange();
    range.selectNodeContents(container.querySelector('strong')!);
    selectRange(range);
    expect(selectedMarkdownParagraph(container)).toMatchObject({ supported: true, text: 'Selected', startOffset: 3 });
  });

  it('captures headings and nested list items without duplicating descendant text', () => {
    const container = document.createElement('div');
    container.innerHTML = '<h2>Title</h2><p>Body</p><ol><li>Parent<ul><li>Child</li></ul></li><li><p>Loose</p></li></ol>';
    document.body.append(container);
    const range = document.createRange(); range.selectNodeContents(container); selectRange(range);
    const selected = selectedMarkdownParagraph(container, true);
    expect(selected?.supported).toBe(true);
    expect(selected?.paragraphSelections?.map(item => item.selectedText)).toEqual(['Title', 'Body', 'Parent', 'Child', 'Loose']);
  });

  it.each(['<pre><code>code</code></pre>', '<table><tbody><tr><td>cell</td></tr></tbody></table>', '<img src="fixture.png">'])('keeps unsupported structures blocked: %s', markup => {
    const container = document.createElement('div');
    container.innerHTML = `<h2>Title</h2>${markup}<p>Body</p>`;
    document.body.append(container);
    const range = document.createRange(); range.selectNodeContents(container); selectRange(range);
    expect(selectedMarkdownParagraph(container, true)?.supported).toBe(false);
  });

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


  it('captures each paragraph when multi-paragraph rewriting is enabled', () => {
    const { container, editable } = paragraphFixture();
    const range = document.createRange(); range.setStart(editable, 0); range.setEnd(editable, 2); selectRange(range);
    const selected = selectedMarkdownParagraph(container, true);
    expect(selected?.supported).toBe(true);
    expect(selected?.paragraphSelections).toHaveLength(2);
  });

  it('keeps a real multi-paragraph selection unsupported', () => {
    const { container, editable } = paragraphFixture();
    const range = document.createRange();
    range.setStart(editable, 0);
    range.setEnd(editable, 2);
    selectRange(range);

    expect(selectedMarkdownParagraph(container)?.supported).toBe(false);
  });

  it('identifies an internal reference selected at element boundaries', () => {
    const container = document.createElement('section');
    container.innerHTML = [
      '<div contenteditable="true">',
      '<p>Alpha <a href="#block-sec-1">beta</a> gamma</p>',
      '</div>',
    ].join('');
    document.body.append(container);
    const paragraph = container.querySelector('p')!;
    const range = document.createRange();
    range.setStart(paragraph, 1);
    range.setEnd(paragraph, 2);
    selectRange(range);

    expect(selectedMarkdownParagraph(container)?.internalReference).toEqual({
      anchorId: 'block-sec-1',
      occurrence: 0,
    });
  });

  it('identifies a partial selection inside an internal reference', () => {
    const container = document.createElement('section');
    container.innerHTML = [
      '<div contenteditable="true">',
      '<p>Alpha <a href="#block-sec-1">beta</a> gamma</p>',
      '</div>',
    ].join('');
    document.body.append(container);
    const linkText = container.querySelector('a')!.firstChild!;
    const range = document.createRange();
    range.setStart(linkText, 1);
    range.setEnd(linkText, 3);
    selectRange(range);

    expect(selectedMarkdownParagraph(container)?.internalReference).toEqual({
      anchorId: 'block-sec-1',
      occurrence: 0,
    });
  });

  it('does not identify a selection crossing an internal reference boundary', () => {
    const container = document.createElement('section');
    container.innerHTML = [
      '<div contenteditable="true">',
      '<p>Alpha <a href="#block-sec-1">beta</a> gamma</p>',
      '</div>',
    ].join('');
    document.body.append(container);
    const paragraph = container.querySelector('p')!;
    const linkText = paragraph.querySelector('a')!.firstChild!;
    const trailingText = paragraph.lastChild!;
    const range = document.createRange();
    range.setStart(linkText, 0);
    range.setEnd(trailingText, 2);
    selectRange(range);

    expect(selectedMarkdownParagraph(container)?.internalReference).toBeUndefined();
  });
});
