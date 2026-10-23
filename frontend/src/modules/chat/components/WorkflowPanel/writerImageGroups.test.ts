import { expect, it } from 'vitest';
import { documentImageGroups, imageGroupMarkdown, keepVerticalImageGroups, markdownImageCandidates, pendingImageGroups, stripImageGroupMarkdown } from './writerImageGroups';

it('ends consecutive image groups at headings, text and other content', () => {
  const markdown = '# Title\n\n![a](a.png)\n\n![b](b.png)\n\n## Chapter\n\n![c](c.png)\n![d](d.png)\n![e](e.png)\n\nBody\n\n![f](f.png)\n\nBody\n\n![g](g.png)';
  expect(markdownImageCandidates(markdown).map(group => group.images.length)).toEqual([2, 3]);
  expect(pendingImageGroups(markdown)).toHaveLength(1);
  const chosen = keepVerticalImageGroups(markdown) as string;
  expect(pendingImageGroups(chosen)).toHaveLength(0);
  expect(documentImageGroups(chosen)[0].layout).toBe('vertical');
  expect(markdownImageCandidates(chosen).map(group => group.images.length)).toEqual([2, 3]);
});

it('tracks repeated image occurrences separately', () => {
  expect(markdownImageCandidates('![a](same.png)\n\n![b](same.png)')[0].images).toEqual([
    { source: 'same.png', occurrence: 0 }, { source: 'same.png', occurrence: 1 },
  ]);
});

it('recognizes image groups across system numbering anchors', () => {
  const source = ['a', 'b', 'c'].map(name => `<a id="block-${name}" data-writer-numbering="auto" />\n![${name}](${name}.png)`).join('\n\n');
  expect(markdownImageCandidates(source)[0].images).toHaveLength(3);
  expect(pendingImageGroups(source)).toHaveLength(1);
});

it('preserves frontmatter and code examples while safely replacing group settings', () => {
  const body = '---\r\ntitle: Example\r\n---\r\n\r\n```html\r\n<!-- writer-image-groups: invalid -->\r\n```\r\n\r\n<!-- ordinary comment -->\r\n';
  const groups = [{ images: [{ source: 'a-->b.png', occurrence: 0 }, { source: 'c.png', occurrence: 0 }], layout: 'sliding' as const }];
  const saved = imageGroupMarkdown(body, groups);
  expect(documentImageGroups(body)).toEqual([]);
  expect(documentImageGroups(saved)).toEqual(groups);
  expect(saved).toContain('a--\\u003eb.png');
  expect(stripImageGroupMarkdown(saved).trimEnd()).toBe(body.trimEnd());
  expect(imageGroupMarkdown(saved, groups)).toBe(saved);
  expect(documentImageGroups(saved.replace(/\r?\n/g, '\r\n'))).toEqual(groups);
  expect(() => documentImageGroups('<!-- writer-image-groups: invalid -->')).toThrow();
  expect(() => documentImageGroups('<!-- writer-image-groups: {} -->')).toThrow();
});
