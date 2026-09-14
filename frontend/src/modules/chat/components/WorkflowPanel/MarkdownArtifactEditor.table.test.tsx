import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';


beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => vi.unstubAllGlobals());

const source = '# Paper\n\nAbstract\n\n## Analysis\n\n| Mechanism | Result |\n| --- | --- |\n| First mechanism | First result |\n| Second mechanism | Second result |\n\nDiscussion';

describe('Markdown table row editing', () => {
  it.each([
    ['Insert a row below this one', 4],
    ['Delete this row', 2],
  ])('retains the editor surface and viewport after %s', async (action, rowCount) => {
    const onSave = vi.fn(async (markdown: string) => ({ markdown, revision: 2 }));
    const { container } = render(<MarkdownArtifactEditor markdown={source} sourceRevision={1} onSave={onSave} />);
    await waitFor(() => expect(container.querySelectorAll('tbody tr')).toHaveLength(3));
    const surface = container.querySelector<HTMLElement>('.writer-markdown-editor__surface')!;
    surface.scrollTop = 180;
    fireEvent.click(screen.getAllByTitle('Row menu')[1]);
    fireEvent.click(await screen.findByTitle(action));
    await waitFor(() => expect(container.querySelectorAll('tbody tr')).toHaveLength(rowCount));
    await waitFor(() => expect(onSave).toHaveBeenCalled(), { timeout: 2500 });
    if (rowCount === 4) expect(onSave.mock.calls[0][0]).toContain('First mechanism');
    else expect(onSave.mock.calls[0][0]).not.toContain('First mechanism');
    expect(onSave.mock.calls[0][0]).toContain('Second mechanism');
    expect(container.querySelector('.writer-markdown-editor__surface')).toBe(surface);
    expect(surface.scrollTop).toBe(180);
  });
});
