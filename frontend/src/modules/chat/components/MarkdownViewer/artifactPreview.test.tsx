import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import MarkdownViewer from './index';
import { ArtifactPreviewContext } from '../ArtifactPreview/context';

const mocks = vi.hoisted(() => ({ download: vi.fn() }));
vi.mock('@/modules/knowledge/utils/imageUrl', async original => ({
  ...await original<typeof import('@/modules/knowledge/utils/imageUrl')>(),
  resolveMarkdownImageUrlAsync: async (url: string) => url,
}));
vi.mock('../../utils/artifactFileActions', async original => ({
  ...await original<typeof import('../../utils/artifactFileActions')>(),
  downloadArtifactFile: mocks.download,
}));
const preview = { target: null, open: vi.fn(), close: vi.fn(), onVersions: undefined };
beforeEach(() => { preview.open.mockReset(); mocks.download.mockReset(); });

it('previews managed files on click while preserving external links and a separate download action', async () => {
  render(<ArtifactPreviewContext.Provider value={preview}>
    <MarkdownViewer previewArtifacts conversationId="conv">
      {'[report.pdf](/static-files/report.pdf)\n\n[Reference](https://example.com/report)'}
    </MarkdownViewer>
  </ArtifactPreviewContext.Provider>);
  fireEvent.click(screen.getByRole('button', { name: 'report.pdf' }));
  expect(preview.open).toHaveBeenCalledWith(expect.objectContaining({ filename: 'report.pdf', conversationId: 'conv' }));
  expect(mocks.download).not.toHaveBeenCalled();
  expect(screen.getByRole('link', { name: 'Reference' })).toHaveAttribute('href', 'https://example.com/report');
  const download = screen.getByRole('button', { name: /下载.*report.pdf/ });
  await waitFor(() => expect(download).toBeEnabled());
  fireEvent.click(download);
  await waitFor(() => expect(mocks.download).toHaveBeenCalled());
  expect(preview.open).toHaveBeenCalledTimes(1);
});

it('opens images with keyboard and retains an entry when the thumbnail fails', async () => {
  render(<ArtifactPreviewContext.Provider value={preview}>
    <MarkdownViewer previewArtifacts>{'![diagram](/static-files/diagram.png)'}</MarkdownViewer>
  </ArtifactPreviewContext.Provider>);
  await waitFor(() => expect(screen.getByAltText('diagram')).toHaveAttribute('src', '/static-files/diagram.png'));
  fireEvent.keyDown(screen.getByRole('button', { name: 'diagram' }), { key: 'Enter' });
  expect(preview.open).toHaveBeenCalledWith(expect.objectContaining({ filename: 'diagram.png' }));
  fireEvent.error(screen.getByAltText('diagram'));
  fireEvent.click(screen.getByRole('button', { name: 'diagram' }));
  expect(preview.open).toHaveBeenCalledTimes(2);
});

it('keeps embedded Workflow images on their original preview behavior by default', async () => {
  render(<ArtifactPreviewContext.Provider value={preview}>
    <MarkdownViewer>{'![workflow image](/static-files/diagram.png)'}</MarkdownViewer>
  </ArtifactPreviewContext.Provider>);
  await act(async () => fireEvent.keyDown(screen.getByRole('button', { name: 'workflow image' }), { key: 'Enter' }));
  expect(preview.open).not.toHaveBeenCalled();
});
