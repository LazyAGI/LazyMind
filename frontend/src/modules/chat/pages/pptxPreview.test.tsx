import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi, it, expect } from 'vitest';
import Page from './pptxPreview';

const api = vi.hoisted(() => ({ getSlotItemVersions: vi.fn() }));
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => api }));
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({ resolveCoreAssetUrl: (url: string) => url }));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: ({ file }: { file: string }) => <div data-testid="viewer">{file}</div> }));

it('opens the requested revision rather than silently using the latest file', async () => {
  api.getSlotItemVersions.mockResolvedValue({ data: { data: { versions: [
    { revision: 1, content_snapshot: { filename: 'first.pptx', url: '/first.pptx' } },
    { revision: 2, content_snapshot: { filename: 'latest.pptx', url: '/latest.pptx' } },
  ] } } });
  render(<MemoryRouter initialEntries={['/workflow-runs/session/pptx/native_pptx?revision=1&index=-1']}>
    <Routes><Route path="/workflow-runs/:sessionId/pptx/:slotId" element={<Page />} /></Routes>
  </MemoryRouter>);
  await waitFor(() => expect(screen.getByRole('link', { name: '下载 PPTX' })).toHaveAttribute('href', '/first.pptx'));
  expect(screen.queryByTestId('viewer')).not.toBeInTheDocument();
  expect(screen.getByText(/网页暂不支持打开 PPTX/)).toBeInTheDocument();
  expect(api.getSlotItemVersions).toHaveBeenCalledWith('session', 'native_pptx', -1, expect.anything());
});
