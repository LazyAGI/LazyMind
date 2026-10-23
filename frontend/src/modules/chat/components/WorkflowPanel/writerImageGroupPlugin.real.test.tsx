import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { beforeAll, expect, it } from 'vitest';
import i18n from '@/i18n';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
import { documentImageGroups, markdownImageCandidates } from './writerImageGroups';

beforeAll(() => i18n.changeLanguage('en-US'));

it('saves, reopens and restores image groups through the Markdown editor', async () => {
  const markdown = '# Article\n\n![First](https://example.com/a.png)\n\n![Second](https://example.com/b.png)\n\nBody\n\n![Single](https://example.com/c.png)';
  let saved = markdown;
  const onSave = async (value: string) => { saved = value; return 2; };
  const view = render(<MarkdownArtifactEditor markdown={markdown} sourceRevision={1} onSave={onSave} />);

  await waitFor(() => expect(view.getByRole('button', { name: 'Set as sliding gallery' })).toBeInTheDocument());
  await act(async () => fireEvent.click(view.getByRole('button', { name: 'Set as sliding gallery' })));
  await waitFor(() => expect(documentImageGroups(saved)[0]?.layout).toBe('sliding'), { timeout: 4000 });
  expect(view.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding');
  expect(saved).toContain('<!-- writer-image-groups:');

  view.unmount();
  const reopened = render(<MarkdownArtifactEditor markdown={saved} sourceRevision={2} onSave={onSave} />);
  await waitFor(() => expect(reopened.container.querySelector('.writer-image-group')).toHaveAttribute('data-layout', 'sliding'));
  await act(async () => fireEvent.click(reopened.getByRole('button', { name: 'Restore vertical layout' })));
  await waitFor(() => expect(documentImageGroups(saved)[0].layout).toBe('vertical'), { timeout: 4000 });
  expect(markdownImageCandidates(saved)[0].images.map(image => image.source)).toEqual([
    'https://example.com/a.png', 'https://example.com/b.png',
  ]);
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
