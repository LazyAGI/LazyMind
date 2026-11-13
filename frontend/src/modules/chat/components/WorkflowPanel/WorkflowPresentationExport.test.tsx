import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { PresentationExportAction } from './ppt/PresentationExportAction';

const api = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }));
vi.mock('@/components/request', () => ({ axiosInstance: api, BASE_URL: '' }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('./ppt/slideAssets', () => ({ embedSlideAssetsForExport: async (html: string) => html }));
vi.mock('./ppt/exportHtmlToPptx', () => ({
  extractHtmlFromArtifact: (value: any) => value.text,
  declaredSlideFontFamilies: () => [],
  exportHtmlSlidesAsRasterPdf: vi.fn(), exportHtmlSlidesAsRasterPptx: vi.fn(),
}));
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({
  resolveCoreAssetUrl: (url: string) => url, resolveMarkdownImageUrlAsync: vi.fn(), isExpiredSignedUrl: () => false,
}));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

it('exports in place without a capability gate and retries a preparation failure', async () => {
  api.post.mockRejectedValueOnce(new Error('Preparation failed; retry')).mockRejectedValueOnce(new Error('Retry reached exporter'));
  render(<PresentationExportAction
    action={{ id: 'export', type: 'export', provider: 'html-presentation', inputs: { pages: 'pages' }, formats: ['editable-pptx'] } as any}
    tab={{ id: 'deck', slots: [{ id: 'pages' }] } as any}
    session={{ session_id: 'run', slots: [{ slot: 'pages', selected: true, sort_order: 0, artifact_value: { text: '<html>Slide</html>' } }] } as any}
    rows={[0]}
  />);
  const exportAgain = async () => {
    fireEvent.click(screen.getByText('chat.workflowExportPptx'));
    fireEvent.click(await screen.findByText('chat.workflowExportModeEditable'));
  };
  await exportAgain();
  expect(await screen.findByText('Preparation failed; retry')).toBeTruthy();
  expect(api.get).not.toHaveBeenCalled();
  expect(screen.queryByText('chat.configureEditablePpt')).toBeNull();
  await exportAgain();
  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(2));
  expect(await screen.findByText('Retry reached exporter')).toBeTruthy();
});
