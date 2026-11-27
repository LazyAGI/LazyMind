import { useState } from 'react';
import { fireEvent, render } from '@testing-library/react';
import { beforeAll, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { WriterIRControl } from './WriterIRControl';
import { WriterIRDocumentEditor } from './WriterIRDocumentEditor';
import type { WriterDocument } from './writerIR';
import { irImageCandidates } from './writerImageGroups';

beforeAll(() => i18n.changeLanguage('en-US'));

it('previews and restores structured images while keeping individual records', () => {
  const source = 'https://example.com/\u56fe\u7247.png';
  const encoded = 'https://example.com/%E5%9B%BE%E7%89%87.png';
  let current: WriterDocument = { document_id: 'article', title: 'City greenery', stage: 'final', blocks: [
    { node_id: 'section', type: 'heading', content: 'Park', children: [
      { node_id: 'single', type: 'image', content: 'Lawn', references: [{ type: 'media_asset', path: source }] },
      { node_id: 'placeholder', type: 'image', content: 'Placeholder' },
      { node_id: 'first', type: 'image', content: 'Path', references: [{ type: 'media_asset', path: encoded }] },
      { node_id: 'second', type: 'image', content: 'Bench', references: [{ type: 'media_asset', path: source }] },
      { node_id: 'text', type: 'paragraph', content: 'Body' },
      { node_id: 'empty-first', type: 'image', content: 'First empty image' },
      { node_id: 'empty-second', type: 'image', content: 'Second empty image' },
    ] },
  ] };
  function Editor() {
    const [document, setDocument] = useState(current);
    return <WriterIRDocumentEditor document={document} ariaLabel='Document' onFocus={() => {}} onBlur={() => {}}
      onChange={value => { current = value; setDocument(value); }} />;
  }
  const { container, getByRole } = render(<Editor />);
  expect(container.querySelectorAll('.writer-image-group')).toHaveLength(1);
  expect(container.querySelectorAll('.writer-ir__image-placeholder')).toHaveLength(3);
  expect(irImageCandidates(current)).toEqual([{
    nodeIds: ['first', 'second'], images: [1, 2].map(occurrence => ({ source: encoded, occurrence })),
  }]);
  expect(current.metadata?.image_groups).toBeUndefined();
  fireEvent.click(getByRole('button', { name: 'Set as sliding gallery' }));
  expect(container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding');
  expect(container.querySelector('.writer-image-group')).toHaveAttribute('data-image-count', '2');
  expect(current.blocks[0].children?.map(block => block.node_id)).toEqual([
    'single', 'placeholder', 'first', 'second', 'text', 'empty-first', 'empty-second',
  ]);
  fireEvent.click(getByRole('button', { name: 'Restore vertical layout' }));
  expect(container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'vertical');
  expect(container.querySelectorAll('img')).toHaveLength(3);
});

it('undoes each structured image drag separately, including a gallery of repeated sources', () => {
  const paragraph = (node_id: string) => ({ node_id, type: 'paragraph', content: node_id });
  const image = (node_id: string) => ({ node_id, type: 'image', content: node_id,
    references: [{ type: 'media_asset', url: 'https://example.com/repeated.png' }] });
  const initial: WriterDocument = { document_id: 'drag-test', title: 'Drag', stage: 'final',
    blocks: [paragraph('first'), image('a'), paragraph('between'), paragraph('second'), image('b')] };
  const { container } = render(<WriterIRControl document={initial} onSave={vi.fn()} />);
  const block = (id: string) => container.querySelector<HTMLElement>(`[data-writer-block][data-node-id="${id}"]`)!;
  const order = () => Array.from(container.querySelectorAll<HTMLElement>('[data-writer-block]'), el => el.dataset.nodeId);
  const hitTest = Object.getOwnPropertyDescriptor(document, 'elementFromPoint');
  const drag = (source: HTMLElement, target: HTMLElement) => {
    Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: () => target });
    vi.spyOn(target, 'getBoundingClientRect').mockReturnValue(new DOMRect(100, 0, 80, 80));
    fireEvent.mouseDown(source, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.mouseMove(window, { clientX: 160, clientY: 40 });
    fireEvent.mouseUp(window, { clientX: 160, clientY: 40 });
  };
  try {
    expect(block('a').querySelector('[data-writer-drag-handle]')).toBeNull();
    drag(block('a').querySelector('img')!, block('between'));
    const moved = ['first', 'between', 'a', 'second', 'b'];
    expect(order()).toEqual(moved);
    drag(block('a').querySelector('img')!, block('b').querySelector('img')!);
    expect(container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding');
    expect(order()).toEqual(['first', 'between', 'second', 'b', 'a']);
    fireEvent.keyDown(block('first'), { ctrlKey: true, key: 'z' });
    expect(order()).toEqual(moved);
    expect(container.querySelector('.writer-image-group')).toBeNull();
    fireEvent.keyDown(block('first'), { ctrlKey: true, key: 'z' });
    expect(order()).toEqual(initial.blocks.map(block => block.node_id));
  } finally {
    vi.restoreAllMocks();
    if (hitTest) Object.defineProperty(document, 'elementFromPoint', hitTest);
    else Reflect.deleteProperty(document, 'elementFromPoint');
  }
});
