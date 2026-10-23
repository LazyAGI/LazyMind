import { useState } from 'react';
import { fireEvent, render } from '@testing-library/react';
import { beforeAll, expect, it } from 'vitest';
import i18n from '@/i18n';
import { WriterIRDocumentEditor } from './WriterIRDocumentEditor';
import type { WriterDocument } from './writerIR';

beforeAll(() => i18n.changeLanguage('en-US'));

it('previews and restores structured images while keeping individual records', () => {
  let current: WriterDocument = { document_id: 'article', title: 'City greenery', stage: 'final', blocks: [
    { node_id: 'section', type: 'heading', content: 'Park', children: [
      { node_id: 'first', type: 'image', content: 'Path', references: [{ type: 'media_asset', path: 'https://example.com/a.jpg' }] },
      { node_id: 'second', type: 'image', content: 'Bench', references: [{ type: 'media_asset', path: 'https://example.com/b.jpg' }] },
      { node_id: 'text', type: 'paragraph', content: 'Body' },
      { node_id: 'single', type: 'image', content: 'Lawn', references: [{ type: 'media_asset', path: 'https://example.com/c.jpg' }] },
    ] },
  ] };
  function Editor() {
    const [document, setDocument] = useState(current);
    return <WriterIRDocumentEditor document={document} ariaLabel='Document' onFocus={() => {}} onBlur={() => {}}
      onChange={value => { current = value; setDocument(value); }} />;
  }
  const { container, getByRole } = render(<Editor />);
  expect(container.querySelectorAll('.writer-image-group')).toHaveLength(1);
  expect(current.metadata?.image_groups).toBeUndefined();
  fireEvent.click(getByRole('button', { name: 'Set as sliding gallery' }));
  expect(container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding');
  expect(container.querySelector('.writer-image-group')).toHaveAttribute('data-image-count', '2');
  expect(current.blocks[0].children?.map(block => block.node_id)).toEqual(['first', 'second', 'text', 'single']);
  fireEvent.click(getByRole('button', { name: 'Restore vertical layout' }));
  expect(container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'vertical');
  expect(container.querySelectorAll('img')).toHaveLength(3);
});
