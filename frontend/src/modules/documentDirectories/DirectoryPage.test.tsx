import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import DirectoryPage from './DirectoryPage';
import * as api from './api';
const t = (key: string) => key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t }) }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'Read failed' }));
vi.mock('./api', () => ({ listDirectories: vi.fn(), getDirectory: vi.fn(), listBatches: vi.fn(), directoryAction: vi.fn(), readDirectoryMail: vi.fn(), readDirectoryLocalFile: vi.fn() }));
vi.mock('./LocalFileReader', () => ({ default: ({ entry }: { entry?: { id: string } }) => entry ? <div data-testid="local-file-preview">{entry.id}</div> : null }));
const directory = { id: 'd', name: 'Contracts', intent: 'Customer A contracts', organization: 'Type', pinned: false, auto_add: true, version: 2, last_used_at: '2026-10-10T00:00:00Z', coverage: [] };
const entry = { id: 'e', resource_key: 'key', source: 'local', namespace: 'host', resource_id: '/a.txt', title: 'Contract A', group_id: '', pinned: false, excluded: false, read_state: 'unread', confirmed: false, access_state: 'unknown' };
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listDirectories).mockResolvedValue([directory]);
  vi.mocked(api.listBatches).mockResolvedValue([]);
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, entries: [entry], groups: [], organizing: false });
  vi.mocked(api.directoryAction).mockResolvedValue({});
});
function view() { return render(<MemoryRouter initialEntries={['/document-directories/d']}><Routes><Route path="/document-directories/:directoryId" element={<DirectoryPage />} /></Routes></MemoryRouter>); }
it('paginates five documents, resets on filtering, and keeps controls for empty results', async () => {
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, groups: [], organizing: false,
    entries: Array.from({ length: 11 }, (_, i) => ({ ...entry, id: `e${i}`, title: `Document ${i + 1}`, confirmed: i > 0 })),
  });
  view(); await screen.findByText('Document 1');
  expect(screen.getAllByRole('button', { name: 'directories.details' })).toHaveLength(5);
  expect(screen.queryByText('Document 6')).not.toBeInTheDocument();
  fireEvent.click(screen.getByTitle('2'));
  expect(screen.getByText('Document 6')).toBeInTheDocument();
  expect(screen.queryByText('Document 1')).not.toBeInTheDocument();
  fireEvent.click(screen.getByTitle('3'));
  expect(screen.getAllByRole('button', { name: 'directories.details' })).toHaveLength(1);
  expect(screen.getByText('Document 11')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: 'directories.allDocuments' }));
  expect(screen.getByText('Document 1')).toBeInTheDocument();
  fireEvent.click(screen.getByTitle('2'));
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'directories.filterState' }));
  fireEvent.click(await screen.findByText('directories.filter_unconfirmed'));
  expect(screen.getByText('Document 1')).toBeInTheDocument();
  expect(screen.getAllByRole('button', { name: 'directories.details' })).toHaveLength(1);
  expect(screen.getByTitle('Next Page')).toHaveAttribute('aria-disabled', 'true');
  fireEvent.change(screen.getByRole('textbox', { name: 'directories.search' }), { target: { value: 'missing' } });
  expect(screen.getByText('directories.noMatchingDocuments')).toBeInTheDocument();
  expect(screen.getByTitle('1')).toBeInTheDocument();
  expect(screen.getByTitle('Next Page')).toHaveAttribute('aria-disabled', 'true');
});

it('opens and records use, pins position without changing group, and excludes a reference', async () => {
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'directories.details' }));
  expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'touch', directory_id: 'd' });
  fireEvent.click(screen.getByRole('button', { name: 'directories.entryPin' }));
  await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'entry_pin', directory_id: 'd', version: 2, entry_id: 'e', pinned: true }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'directories.exclude' })).not.toBeDisabled());
  fireEvent.click(screen.getByRole('button', { name: 'directories.exclude' }));
  await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'exclude', directory_id: 'd', version: 2, entry_id: 'e' }));
});

it('loads excluded references separately and restores an item beyond the first page', async () => {
  let restored = false;
  vi.mocked(api.getDirectory).mockImplementation(async (_, options) => ({
    directory: { ...directory, version: restored ? 3 : 2 }, groups: [], organizing: false,
    entries: options?.state === 'excluded'
      ? (restored ? [] : [{ ...entry, id: options.offset ? 'excluded-last' : 'excluded-first',
        title: options.offset ? 'Excluded last' : 'Excluded first', excluded: true }])
      : [entry],
    total: options?.state === 'excluded' ? (restored ? 0 : 21) : 1,
  }));
  vi.mocked(api.directoryAction).mockImplementation(async command => {
    if (command.operation === 'restore') restored = true;
    return {};
  });
  view();
  await screen.findByText('Contract A');
  fireEvent.click(screen.getByText('directories.excluded'));
  await screen.findByText('Excluded first');
  fireEvent.click(screen.getByTitle('2'));
  await screen.findByText('Excluded last');
  expect(api.getDirectory).toHaveBeenCalledWith('d', { offset: 20, limit: 20, state: 'excluded' });
  fireEvent.click(screen.getByRole('button', { name: 'directories.restore' }));
  await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith({
    operation: 'restore', directory_id: 'd', version: 2, entry_id: 'excluded-last',
  }));
  await waitFor(() => expect(screen.queryByText('Excluded last')).not.toBeInTheDocument());
});
it('shows committed documents but blocks manual edits during organization', async () => {
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, entries: [entry], groups: [], organizing: true });
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'directories.details' }));
  expect(screen.getByText('directories.organizing')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'directories.entryPin' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'directories.organize' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'directories.exclude' })).toBeDisabled();
  expect(screen.queryByRole('button', { name: 'directories.ask' })).not.toBeInTheDocument();
});
it('hides completed history and retries only the latest failed organization', async () => {
  vi.mocked(api.listBatches).mockResolvedValue([
    { id: 'old', parent_id: '', directory_id: 'd', status: 'failed', acknowledged: false, created_at: '2026-10-09T00:00:00Z' },
    { id: 'done', parent_id: '', directory_id: 'd', status: 'succeeded', acknowledged: false, created_at: directory.last_used_at },
  ]);
  const mounted = view(); await screen.findByText('Contract A');
  expect(screen.queryByText('directories.results')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'directories.ack' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'directories.retry' })).not.toBeInTheDocument();
  mounted.unmount();
  vi.mocked(api.listBatches).mockResolvedValue([
    { id: 'failed', parent_id: '', directory_id: 'd', status: 'failed', acknowledged: false, created_at: directory.last_used_at },
  ]);
  view(); fireEvent.click(await screen.findByRole('button', { name: 'directories.retry' }));
  await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'retry', batch_id: 'failed', directory_id: 'd', version: 2 }));
});

it('pins and unpins a directory from its card without opening it', async () => {
  vi.mocked(api.listBatches).mockResolvedValue([{ id: 'acknowledged', parent_id: '', directory_id: '', status: 'failed', acknowledged: true, created_at: directory.last_used_at }]);
  const pinned = { ...directory, pinned: true, version: 3 };
  vi.mocked(api.directoryAction).mockImplementation(async command => {
    vi.mocked(api.listDirectories).mockResolvedValue([command.pinned ? pinned : directory]);
    return {};
  });
  render(<MemoryRouter><DirectoryPage /></MemoryRouter>);
  const pin = await screen.findByRole('button', { name: 'directories.directoryPin' });
  expect(pin).toHaveAttribute('aria-pressed', 'false');
  expect(screen.queryByRole('button', { name: 'directories.retry' })).not.toBeInTheDocument();
  fireEvent.click(pin);
  const unpin = await screen.findByRole('button', { name: 'directories.directoryUnpin' });
  expect(unpin).toHaveAttribute('aria-pressed', 'true');
  expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'update', directory_id: 'd', version: 2, pinned: true });
  await waitFor(() => expect(unpin).not.toBeDisabled());
  fireEvent.click(unpin);
  await screen.findByRole('button', { name: 'directories.directoryPin' });
  expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'update', directory_id: 'd', version: 3, pinned: false });
});

it('opens legacy Notion URLs and preserves the source link after refresh', async () => {
  const url = 'https://app.notion.com/p/3f4b8b30a5b08119bd8bcf0b4999c02f';
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, entries: [{ ...entry, source: 'notion', locator: { id: url } }], groups: [], organizing: false });
  view();
  expect(await screen.findByRole('link', { name: 'Contract A' })).toHaveAttribute('href', url);
});

it('filters groups and searches without changing references, and explains evidence in details', async () => {
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, organizing: false,
    groups: [{ id: 'g', name: 'Plans', parent_id: '', position: 0 }],
    entries: [{ ...entry, group_id: 'g', evidence: { reason: 'Read the project scope' } },
      { ...entry, id: 'e2', title: 'Meeting notes', resource_id: '/meeting.txt' }],
  });
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: /Plans/ }));
  expect(screen.queryByText('Meeting notes')).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('textbox', { name: 'directories.search' }), { target: { value: 'missing' } });
  expect(screen.queryByText('Contract A')).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('textbox', { name: 'directories.search' }), { target: { value: 'Contract' } });
  fireEvent.click(screen.getByRole('button', { name: 'directories.details' }));
  expect(await screen.findByText('Read the project scope')).toBeInTheDocument();
  expect(screen.getByText('directories.confirmationHelp')).toBeInTheDocument();
  expect(api.directoryAction).toHaveBeenCalledTimes(1);
});

it('filters denied or unavailable references without treating unknown access as a failure', async () => {
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, organizing: false, groups: [], entries: [
    { ...entry, access_state: 'denied' }, { ...entry, id: 'e2', title: 'Unknown access' },
  ] });
  view(); await screen.findByText('Contract A');
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'directories.filterState' }));
  fireEvent.click(await screen.findByText('directories.filter_inaccessible'));
  expect(screen.getByText('Contract A')).toBeInTheDocument();
  expect(screen.queryByText('Unknown access')).not.toBeInTheDocument();
  expect(screen.getByText('directories.access_denied')).toBeInTheDocument();
});

it('shows explicit source failures, hides recovered requests and ignores incomplete or empty searches', async () => {
  const search = { source: 'notion', operation: 'search', query: 'contracts', scope: { query: 'contracts' } };
  vi.mocked(api.getDirectory).mockResolvedValue({ directory: { ...directory, coverage: [
    { ...search, status: 'denied' },
    { ...search, query: 'other', status: 'success' },
    { source: 'mail', operation: 'search', status: 'zero', scope: { query: 'contracts' } },
    { source: 'feishu', operation: 'search', status: 'partial', incomplete: true, scope: { query: 'contracts' } },
    { source: 'capture', operation: 'coverage', status: 'incomplete', incomplete: true },
    { source: 'local', operation: 'read', status: 'unknown', incomplete: true, scope: { path: '/b.txt' } },
    { source: 'local', operation: 'read', status: 'failed', scope: {} },
  ] }, entries: [entry], groups: [], organizing: false });
  const mounted = view(); await screen.findByText('Contract A');
  expect(screen.getByText('directories.sourceIssue_search_denied')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /directories.coverage/ })).not.toBeInTheDocument();
  expect(screen.queryByText('directories.inaccessibleDocuments')).not.toBeInTheDocument();
  expect(screen.queryByText('directories.sourceIssue_read_failed')).not.toBeInTheDocument();
  mounted.unmount();
  vi.mocked(api.getDirectory).mockResolvedValue({ directory: { ...directory, coverage: [
    { ...search, status: 'denied' }, { ...search, status: 'zero' },
    { source: 'local', operation: 'read', status: 'error', scope: { path: '/a.txt' } },
    { source: 'local', operation: 'read', status: 'error', scope: { path: '/a.txt' } },
  ] }, entries: [{ ...entry, access_state: 'denied' }], groups: [], organizing: false });
  view(); await screen.findByText('Contract A');
  expect(screen.queryByText('directories.sourceIssue_search_denied')).not.toBeInTheDocument();
  expect(screen.getAllByText('directories.sourceIssue_read_failed')).toHaveLength(1);
  expect(screen.getByText('directories.inaccessibleDocuments')).toBeInTheDocument();
});

it('separates all documents from groups and preserves filters when switching views', async () => {
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, organizing: false,
    groups: [{ id: 'g', name: 'Plans', parent_id: '', position: 0 }],
    entries: [{ ...entry, group_id: 'g' }, { ...entry, id: 'e2', title: 'Meeting notes' }],
  });
  view(); await screen.findByText('Contract A');
  expect(screen.queryByText('Meeting notes')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: 'directories.allDocuments' }));
  expect(screen.getByText('Meeting notes')).toBeInTheDocument();
  expect(screen.queryByRole('navigation', { name: 'directories.group' })).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('textbox', { name: 'directories.search' }), { target: { value: 'Contract' } });
  fireEvent.click(screen.getByRole('tab', { name: 'directories.groupedView' }));
  expect(screen.getByRole('textbox', { name: 'directories.search' })).toHaveValue('Contract');
  expect(screen.getByRole('button', { name: /Plans/ })).toHaveAttribute('aria-pressed', 'true');
});

it('hides empty ungrouped navigation and reads mail from both title and details', async () => {
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, organizing: true,
    groups: [{ id: 'g', name: 'Mail', parent_id: '', position: 0 }],
    entries: [{ ...entry, source: 'mail', group_id: 'g' }],
  });
  vi.mocked(api.readDirectoryMail).mockResolvedValue({ subject: 'Email original', from: 'sender@example.com', to: 'user@example.com', cc: '', date: '', mailbox: 'user@example.com', body: 'Original body', attachments: [], truncated: true });
  view(); await screen.findByText('Contract A');
  expect(screen.queryByRole('button', { name: /directories.unclassified/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Contract A' }));
  expect(await screen.findByText('Original body')).toBeInTheDocument();
  expect(screen.getByText('directories.mailTruncated')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  fireEvent.click(screen.getByRole('button', { name: 'directories.details' }));
  fireEvent.click(screen.getByRole('button', { name: 'directories.viewMail' }));
  expect(await screen.findByText('Original body')).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'directories.details' })).not.toBeInTheDocument());
  expect(api.directoryAction).toHaveBeenCalledTimes(1);
});


it('falls back to the next nonempty group after a background refresh removes the selection', async () => {
  let tick: () => void = () => {};
  const original = window.setInterval;
  const interval = vi.spyOn(window, 'setInterval').mockImplementation(((callback: () => void, delay: number) => {
    if (delay === 5000) tick = callback;
    return original(callback, delay);
  }) as typeof window.setInterval);
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, organizing: false,
    groups: [{ id: 'g', name: 'Original group', parent_id: '', position: 0 }],
    entries: [{ ...entry, group_id: 'g' }],
  });
  view(); interval.mockRestore();
  await screen.findByText('Contract A');
  vi.mocked(api.getDirectory).mockResolvedValue({ directory, organizing: false,
    groups: [{ id: 'h', name: 'Remaining group', parent_id: '', position: 0 }],
    entries: [{ ...entry, group_id: 'h' }],
  });
  await act(async () => tick());
  await waitFor(() => expect(screen.getByRole('button', { name: /Remaining group/ })).toHaveAttribute('aria-pressed', 'true'));
  expect(screen.getByText('Contract A')).toBeInTheDocument();
});

it('opens local preview from the title while keeping reference details separate', async () => {
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'Contract A' }));
  expect(screen.getByTestId('local-file-preview')).toHaveTextContent('e');
  expect(api.readDirectoryMail).not.toHaveBeenCalled();
});
