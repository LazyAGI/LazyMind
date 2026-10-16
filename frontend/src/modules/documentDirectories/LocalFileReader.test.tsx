import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import LocalFileReader from './LocalFileReader';
import { readDirectoryLocalFile } from './api';
import { hasDesktopFileBridge, showItemInFolder } from '@/runtime/desktopBridge';
const t = (key: string) => key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t }) }));
vi.mock('./api', () => ({ readDirectoryLocalFile: vi.fn() }));
vi.mock('@/runtime/desktopBridge', () => ({ hasDesktopFileBridge: vi.fn(), showItemInFolder: vi.fn() }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'Read failed' }));
vi.mock('@/modules/knowledge/components/FileViewer', () => ({ default: ({ fileName }: { fileName: string }) => <div data-testid="viewer">{fileName}</div> }));
const entry = { id: 'e', resource_key: 'key', source: 'local', namespace: 'host:nt', resource_id: 'C:\\资料\\a.md', title: 'a.md', group_id: '', pinned: false, excluded: false, read_state: 'unread', confirmed: false, access_state: 'accessible' };
beforeEach(() => {
  vi.clearAllMocks();
  URL.createObjectURL = vi.fn(() => 'blob:preview'); URL.revokeObjectURL = vi.fn();
  vi.mocked(hasDesktopFileBridge).mockReturnValue(false);
  vi.mocked(readDirectoryLocalFile).mockResolvedValue({ filename: 'a.md', content_base64: btoa('# document'), mime_type: 'text/markdown', namespace: 'host:nt', path: entry.resource_id, size: 10 });
});
it('previews through owned IDs, hides native controls in a browser and revokes the blob', async () => {
  const view = render(<LocalFileReader directoryId="d" entry={entry} onClose={() => {}} />);
  expect(await screen.findByTestId('viewer')).toHaveTextContent('a.md');
  expect(readDirectoryLocalFile).toHaveBeenCalledWith('d', 'e', expect.any(AbortSignal));
  expect(screen.queryByRole('button', { name: 'directories.showInFolder' })).not.toBeInTheDocument();
  view.unmount(); expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:preview');
});
it('shows a platform-neutral reveal action and forwards IDs rather than a Windows path', async () => {
  vi.mocked(hasDesktopFileBridge).mockReturnValue(true);
  vi.mocked(showItemInFolder).mockResolvedValue({ ok: true });
  render(<LocalFileReader directoryId="d" entry={entry} onClose={() => {}} />);
  await screen.findByTestId('viewer');
  fireEvent.click(screen.getByRole('button', { name: 'directories.showInFolder' }));
  await waitFor(() => expect(showItemInFolder).toHaveBeenCalledWith({ source: '', directoryEntry: { directoryId: 'd', entryId: 'e' } }));
});
it('reports missing files and supports retry without showing stale content', async () => {
  vi.mocked(readDirectoryLocalFile).mockResolvedValueOnce({ filename: '', path: '', namespace: '', mime_type: '', content_base64: '', size: 0, error_code: 'not_found' });
  render(<LocalFileReader directoryId="d" entry={entry} onClose={() => {}} />);
  expect(await screen.findByText('directories.localFile_not_found')).toBeInTheDocument();
  expect(screen.queryByTestId('viewer')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'directories.retry' }));
  expect(await screen.findByTestId('viewer')).toHaveTextContent('a.md');
});
