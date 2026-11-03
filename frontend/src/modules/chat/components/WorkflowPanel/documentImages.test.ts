import { describe, expect, it } from 'vitest';
import { documentImages } from './documentImages';

describe('article image inventory', () => {
  it('uses Markdown images and HTML images, excluding links, code and removed images', () => {
    const content = '# Article\n![First](asset://first)\n![Repeat][one]\n\n[one]: asset://first\n\n[link](unused.png)\n\n```md\n![example](unused.png)\n```\n`![code](unused.png)`\n\n<img src="second.png" alt="Second">';
    expect(documentImages(content)).toEqual([{ source: 'asset://first', alt: 'First' }, { source: 'second.png', alt: 'Second' }]);
    expect(documentImages('# Deleted all pictures')).toEqual([]);
  });

  it('traverses nested IR images and retains the original reference independently of previews', () => {
    expect(documentImages({ document_id: 'doc', stage: 'draft', title: 'Article', blocks: [
      { node_id: 'section', type: 'section', children: [{ node_id: 'image', type: 'image', content: 'Photo', references: [
        { type: 'preview_asset', url: 'https://example.org/preview.png' }, { type: 'media_asset', id: 'original', path: 'assets/original.png' },
      ] }] },
    ] })).toEqual([{ source: 'asset://original', alt: 'Photo', preview: 'https://example.org/preview.png' }]);
  });
});
