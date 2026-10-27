import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { beforeAll, expect, it } from 'vitest';
import i18n from '@/i18n';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
import { documentImageGroups, imageGroupMarkdown, markdownImageCandidates, pendingImageGroups } from './writerImageGroups';

beforeAll(() => i18n.changeLanguage('en-US'));

it('saves, reopens and restores image groups through the Markdown editor', async () => {
  const source = 'https://example.com/\u56fe\u7247%2Fname.png?label=\u82b1&mode=raw%20value';
  const encoded = 'https://example.com/%E5%9B%BE%E7%89%87%2Fname.png?label=%E8%8A%B1&mode=raw%20value';
  const markdown = `# Article\n\n![First](${source})\n\n<img src="${encoded}" width="120" alt="Second" />\n\n![Third](${encoded})\n\nBody\n\n![Single](https://example.com/c.png)`;
  let saved = markdown;
  const onSave = async (value: string) => { saved = value; return 2; };
  const view = render(<MarkdownArtifactEditor markdown={markdown} sourceRevision={1} onSave={onSave} />);

  await waitFor(() => expect(view.getByRole('button', { name: 'Set as sliding gallery' })).toBeInTheDocument());
  expect(view.container.querySelector('.writer-image-group')).toHaveAttribute('data-image-count', '3');
  expect(pendingImageGroups(markdown)).toHaveLength(1);
  await act(async () => fireEvent.click(view.getByRole('button', { name: 'Set as sliding gallery' })));
  await waitFor(() => expect(documentImageGroups(saved)[0]?.layout).toBe('sliding'), { timeout: 4000 });
  expect(view.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding');
  expect(saved).toContain('<!-- writer-image-groups:');
  expect(documentImageGroups(saved)[0].images).toEqual(markdownImageCandidates(saved)[0].images);
  expect(pendingImageGroups(saved)).toHaveLength(0);

  view.unmount();
  const reopened = render(<MarkdownArtifactEditor markdown={saved} sourceRevision={2} onSave={onSave} />);
  await waitFor(() => expect(reopened.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding'));
  await act(async () => fireEvent.click(reopened.getByRole('button', { name: 'Restore vertical layout' })));
  await waitFor(() => expect(documentImageGroups(saved)[0].layout).toBe('vertical'), { timeout: 4000 });
  expect(markdownImageCandidates(saved)[0].images).toEqual([0, 1, 2].map(occurrence => ({ source: encoded, occurrence })));
  expect(saved).toContain('![Single](https://example.com/c.png)');
});

it('groups images separated by line breaks and preserves existing frontmatter', async () => {
  const markdown = '---\ntitle: Example\n---\n\n![First](https://example.com/a.png)\n![Second](https://example.com/b.png)\n\nBody\n\n![Single](https://example.com/c.png)\n';
  let saved = markdown;
  const view = render(<MarkdownArtifactEditor markdown={markdown} sourceRevision={1}
    onSave={async value => { saved = value; return 2; }} />);

  await waitFor(() => expect(view.getByRole('button', { name: 'Set as sliding gallery' })).toBeInTheDocument());
  await act(async () => fireEvent.click(view.getByRole('button', { name: 'Set as sliding gallery' })));
  await waitFor(() => expect(documentImageGroups(saved)[0]?.layout).toBe('sliding'), { timeout: 4000 });
  expect(saved).toMatch(/^---\ntitle: Example\n---/);
  expect(markdownImageCandidates(saved)[0].images).toHaveLength(2);
  expect(view.container.querySelectorAll('.writer-image-group')).toHaveLength(1);
});

it('updates metadata-only revisions without replacing the images or saving an edit', async () => {
  const content = '![First](https://example.com/a.png)\n\n![Second](https://example.com/b.png)';
  const images = markdownImageCandidates(content)[0].images;
  const sliding = imageGroupMarkdown(content, [{ images, layout: 'sliding' }]);
  const vertical = imageGroupMarkdown(content, [{ images, layout: 'vertical' }]);
  const saved: string[] = [];
  const onSave = async (value: string) => { saved.push(value); return 4; };
  const view = render(<MarkdownArtifactEditor markdown={sliding} sourceRevision={1} onSave={onSave} />);
  await waitFor(() => expect(view.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding'));
  const firstImage = view.container.querySelector('img');
  view.rerender(<MarkdownArtifactEditor markdown={vertical} sourceRevision={2} onSave={onSave} />);
  await waitFor(() => expect(view.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'vertical'));
  expect(view.getByRole('button', { name: 'Set as sliding gallery' })).toBeInTheDocument();
  view.rerender(<MarkdownArtifactEditor markdown={sliding} sourceRevision={3} onSave={onSave} />);
  await waitFor(() => expect(view.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding'));
  expect(view.getByRole('button', { name: 'Restore vertical layout' })).toBeInTheDocument();
  expect(view.container.querySelector('img')).toBe(firstImage);
  expect(saved).toEqual([]);
});
