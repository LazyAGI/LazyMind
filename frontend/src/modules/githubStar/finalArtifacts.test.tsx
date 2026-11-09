import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { TaskArtifactList } from '@/modules/chat/components/TaskCenter/TaskArtifactList';
import { ArtifactPreviewContext } from '@/modules/chat/components/ArtifactPreview/context';
import type { PublicArtifact } from '@/modules/chat/types/ordinaryTask';

vi.mock('react-i18next', async original => ({
  ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: () => <div>Fixture file viewer</div> }));
afterEach(cleanup);
const artifact: PublicArtifact = {
  artifact_id: 'report', revision: 1, producer_display_key: 'work-root', name: 'Report.md',
  content_type: 'text/markdown', size_bytes: null, state: 'ready', preview_kind: 'text',
  capabilities: { preview: true, open: false, download: false }, inline_content: '# Complete report',
  created_at: '2026-11-04T02:00:00Z',
};

it.each([true, false])('the real artifact list opens its preview without passing a Star opportunity (final=%s)', final => {
  const preview = { target: null, open: vi.fn(), close: vi.fn(), onVersions: undefined };
  render(<ArtifactPreviewContext.Provider value={preview}>
    <TaskArtifactList artifacts={[artifact]} final={final} />
  </ArtifactPreviewContext.Provider>);
  fireEvent.click(screen.getByRole('button', { name: 'Report.md' }));
  expect(preview.open).toHaveBeenCalledTimes(1);
  expect(preview.open).toHaveBeenCalledWith(expect.objectContaining({ content: '# Complete report' }));
  expect(preview.open.mock.calls[0][0]).not.toHaveProperty('starOpportunity');
});
