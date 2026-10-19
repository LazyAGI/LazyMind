import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
function mockDirectory(detail: api.DocumentDirectoryDetail) {
  vi.mocked(api.getDirectory).mockImplementation(async (_id, page = {}) => {
    const active = detail.entries.filter(e => e.excluded === (page.state === 'excluded'));
    const group_counts: Record<string, number> = {};
    active.forEach(e => { group_counts[e.group_id] = (group_counts[e.group_id] || 0) + 1; });
    const query = (page.query || '').trim().toLowerCase();
    const matches = active.filter(e =>
      (page.groupId === undefined || e.group_id === page.groupId) &&
      (e.title.toLowerCase().includes(query) || e.resource_id.toLowerCase().includes(query)) &&
      (page.state === 'unconfirmed' ? !e.confirmed : page.state === 'unread' ? e.read_state === 'unread' :
        page.state === 'inaccessible' ? ['denied', 'deleted', 'unavailable'].includes(e.access_state) : true));
    const offset = page.offset || 0;
    return { ...detail, group_counts, total: matches.length, entries: matches.slice(offset, offset + (page.limit || 5)) };
  });
}
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listDirectories).mockResolvedValue([directory]);
  vi.mocked(api.listBatches).mockResolvedValue([]);
  mockDirectory({ directory, entries: [entry], groups: [], organizing: false });
  vi.mocked(api.directoryAction).mockResolvedValue({});
});
function view() { return render(<MemoryRouter initialEntries={['/document-directories/d']}><Routes><Route path="/document-directories/:directoryId" element={<DirectoryPage />} /></Routes></MemoryRouter>); }
it('paginates five documents, resets on filtering, and keeps controls for empty results', async () => {
  mockDirectory({ directory, groups: [], organizing: false,
    entries: Array.from({ length: 11 }, (_, i) => ({ ...entry, id: `e${i}`, title: `Document ${i + 1}`, confirmed: i > 0 })),
  });
  view(); await screen.findByText('Document 1');
  expect(screen.getAllByRole('button', { name: 'directories.details' })).toHaveLength(5);
  expect(screen.queryByText('Document 6')).not.toBeInTheDocument();
  fireEvent.click(screen.getByTitle('2'));
  expect(await screen.findByText('Document 6')).toBeInTheDocument();
  expect(screen.queryByText('Document 1')).not.toBeInTheDocument();
  fireEvent.click(screen.getByTitle('3'));
  await screen.findByText('Document 11');
  expect(screen.getAllByRole('button', { name: 'directories.details' })).toHaveLength(1);
  expect(screen.getByText('Document 11')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: 'directories.allDocuments' }));
  expect(await screen.findByText('Document 1')).toBeInTheDocument();
  fireEvent.click(screen.getByTitle('2'));
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'directories.filterState' }));
  fireEvent.click(await screen.findByText('directories.filter_unconfirmed'));
  expect(await screen.findByText('Document 1')).toBeInTheDocument();
  expect(screen.getAllByRole('button', { name: 'directories.details' })).toHaveLength(1);
  await waitFor(() => expect(screen.getByTitle('Next Page')).toHaveAttribute('aria-disabled', 'true'));
  fireEvent.change(screen.getByRole('textbox', { name: 'directories.search' }), { target: { value: 'missing' } });
  expect(screen.getByText('directories.noMatchingDocuments')).toBeInTheDocument();
  expect(screen.getByTitle('1')).toBeInTheDocument();
  await waitFor(() => expect(screen.getByTitle('Next Page')).toHaveAttribute('aria-disabled', 'true'));
});

it('opens and records use, pins position without changing group, and excludes a reference', async () => {
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'directories.details' }));
  expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'touch', directory_id: 'd' });
  expect(screen.queryByRole('button', { name: 'directories.refreshSources' })).not.toBeInTheDocument();
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
  fireEvent.click(screen.getByRole('button', { name: 'directories.more' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'directories.excluded' }));
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
  mockDirectory({ directory, entries: [entry], groups: [], organizing: true });
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'directories.details' }));
  expect(screen.getByText('directories.organizing')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'directories.entryPin' })).toBeDisabled();
  expect(screen.queryByRole('button', { name: 'directories.organize' })).not.toBeInTheDocument();
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
  mockDirectory({ directory, entries: [{ ...entry, source: 'notion', locator: { id: url } }], groups: [], organizing: false });
  view();
  expect(await screen.findByRole('link', { name: 'Contract A' })).toHaveAttribute('href', url);
});

it('filters groups and searches without changing references, and explains evidence in details', async () => {
  mockDirectory({ directory, organizing: false,
    groups: [{ id: 'g', name: 'Plans', parent_id: '', position: 0 }],
    entries: [{ ...entry, group_id: 'g', evidence: { reason: 'Read the project scope' } },
      { ...entry, id: 'e2', title: 'Meeting notes', resource_id: '/meeting.txt' }],
  });
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: /Plans/, pressed: true }));
  expect(screen.queryByText('Meeting notes')).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('textbox', { name: 'directories.search' }), { target: { value: 'missing' } });
  expect(screen.queryByText('Contract A')).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('textbox', { name: 'directories.search' }), { target: { value: 'Contract' } });
  fireEvent.click(await screen.findByRole('button', { name: 'directories.details' }));
  expect(await screen.findByText('Read the project scope')).toBeInTheDocument();
  expect(screen.getByText('directories.confirmationHelp')).toBeInTheDocument();
  expect(api.directoryAction).toHaveBeenCalledTimes(1);
});

it('filters denied or unavailable references without treating unknown access as a failure', async () => {
  mockDirectory({ directory, organizing: false, groups: [], entries: [
    { ...entry, access_state: 'denied' }, { ...entry, id: 'e2', title: 'Unknown access' },
  ] });
  view(); await screen.findByText('Contract A');
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'directories.filterState' }));
  fireEvent.click(await screen.findByText('directories.filter_inaccessible'));
  expect(await screen.findByText('Contract A')).toBeInTheDocument();
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
  mockDirectory({ directory, organizing: false,
    groups: [{ id: 'g', name: 'Plans', parent_id: '', position: 0 }],
    entries: [{ ...entry, group_id: 'g' }, { ...entry, id: 'e2', title: 'Meeting notes' }],
  });
  view(); await screen.findByText('Contract A');
  expect(screen.queryByText('Meeting notes')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: 'directories.allDocuments' }));
  expect(await screen.findByText('Meeting notes')).toBeInTheDocument();
  expect(screen.queryByRole('navigation', { name: 'directories.group' })).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole('textbox', { name: 'directories.search' }), { target: { value: 'Contract' } });
  fireEvent.click(screen.getByRole('tab', { name: 'directories.groupedView' }));
  expect(screen.getByRole('textbox', { name: 'directories.search' })).toHaveValue('Contract');
  expect(screen.getByRole('button', { name: /Plans/, pressed: true })).toHaveAttribute('aria-pressed', 'true');
});

it('hides empty ungrouped navigation and reads mail from both title and details', async () => {
  mockDirectory({ directory, organizing: true,
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
  const original = window.setTimeout;
  const interval = vi.spyOn(window, 'setTimeout').mockImplementation(((callback: () => void, delay: number) => {
    if (delay === 5000) tick = callback;
    return original(callback, delay);
  }) as typeof window.setTimeout);
  mockDirectory({ directory, organizing: false,
    groups: [{ id: 'g', name: 'Original group', parent_id: '', position: 0 }],
    entries: [{ ...entry, group_id: 'g' }],
  });
  view();
  await screen.findByText('Contract A');
  interval.mockRestore();
  mockDirectory({ directory, organizing: false,
    groups: [{ id: 'h', name: 'Remaining group', parent_id: '', position: 0 }],
    entries: [{ ...entry, group_id: 'h' }],
  });
  await act(async () => tick());
  await waitFor(() => expect(screen.getByRole('button', { name: /Remaining group/, pressed: true })).toHaveAttribute('aria-pressed', 'true'));
  expect(await screen.findByText('Contract A')).toBeInTheDocument();
});

it('opens local preview from the title while keeping reference details separate', async () => {
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'Contract A' }));
  expect(screen.getByTestId('local-file-preview')).toHaveTextContent('e');
  expect(api.readDirectoryMail).not.toHaveBeenCalled();
});

it('keeps the server page intact and never renders unresolved groups or stale search responses', async () => {
  const groups = [{ id: 'g', name: 'Plans', parent_id: '', position: 0 }];
  const pending = new Map<string, (value: api.DocumentDirectoryDetail) => void>();
  const result = (title: string): api.DocumentDirectoryDetail => ({ directory, groups, organizing: false,
    group_counts: { g: 1 }, total: 1, entries: [{ ...entry, group_id: 'g', title }] });
  vi.mocked(api.getDirectory).mockImplementation((_id, page = {}) => {
    if (page.groupId === undefined) return Promise.resolve(result('Unscoped initial page'));
    return new Promise(resolve => { pending.set(page.query || '', resolve); });
  });
  view();
  await waitFor(() => expect(pending.has('')).toBe(true));
  expect(screen.queryByText('Unscoped initial page')).not.toBeInTheDocument();
  await act(async () => pending.get('')!(result('Initial document')));
  const search = screen.getByRole('textbox', { name: 'directories.search' });
  fireEvent.change(search, { target: { value: 'old' } });
  expect(screen.queryByText('Initial document')).not.toBeInTheDocument();
  await waitFor(() => expect(pending.has('old')).toBe(true));
  fireEvent.change(search, { target: { value: 'new' } });
  await waitFor(() => expect(pending.has('new')).toBe(true));
  // The API owns matching; the component must display its current page unchanged.
  await act(async () => pending.get('new')!(result('Server-selected result')));
  expect(screen.getByText('Server-selected result')).toBeInTheDocument();
  await act(async () => pending.get('old')!(result('Stale result')));
  expect(screen.queryByText('Stale result')).not.toBeInTheDocument();
  expect(screen.getByText('Server-selected result')).toBeInTheDocument();
});

it('resets page on group and state changes, ignoring pending results from the old group', async () => {
  const groups = [{ id: 'g', name: 'Plans', parent_id: '', position: 0 }, { id: 'h', name: 'Notes', parent_id: '', position: 1 }];
  mockDirectory({ directory, groups, organizing: false, entries: [
    ...Array.from({ length: 6 }, (_, i) => ({ ...entry, id: `p${i}`, group_id: 'g', title: `Plan ${i + 1}` })),
    { ...entry, id: 'note', group_id: 'h', title: 'Note', read_state: 'read' },
  ] });
  const paged = vi.mocked(api.getDirectory).getMockImplementation()!;
  let oldPage!: (value: api.DocumentDirectoryDetail) => void;
  vi.mocked(api.getDirectory).mockImplementation((id, page = {}) => page.groupId === 'g' && page.offset === 5
    ? new Promise(resolve => { oldPage = resolve; }) : paged(id, page));
  view(); await screen.findByText('Plan 1');
  fireEvent.click(screen.getByTitle('2'));
  await waitFor(() => expect(oldPage).toBeDefined());
  fireEvent.click(screen.getByRole('button', { name: /Notes/, pressed: false }));
  await screen.findByText('Note');
  expect(api.getDirectory).toHaveBeenLastCalledWith('d', expect.objectContaining({ groupId: 'h', offset: 0 }));
  await act(async () => oldPage({ directory, groups, organizing: false, group_counts: { g: 6, h: 1 }, total: 6,
    entries: [{ ...entry, group_id: 'g', title: 'Late Plan 6' }] }));
  expect(screen.queryByText('Late Plan 6')).not.toBeInTheDocument();
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'directories.filterState' }));
  fireEvent.click(await screen.findByText('directories.filter_unread'));
  await waitFor(() => expect(api.getDirectory).toHaveBeenLastCalledWith('d', expect.objectContaining({ groupId: 'h', offset: 0, state: 'unread' })));
  await waitFor(() => expect(screen.queryByText('Note')).not.toBeInTheDocument());
});

it('loads the preceding server page after excluding the last item on the last page', async () => {
  const data = { directory, groups: [], organizing: false,
    entries: Array.from({ length: 6 }, (_, i) => ({ ...entry, id: `e${i}`, title: `Document ${i + 1}` })) };
  mockDirectory(data);
  vi.mocked(api.directoryAction).mockImplementation(async command => {
    if (command.operation === 'exclude') data.entries = data.entries.filter(e => e.id !== command.entry_id);
    return {};
  });
  view(); await screen.findByText('Document 1');
  fireEvent.click(screen.getByTitle('2'));
  await screen.findByText('Document 6');
  fireEvent.click(screen.getByRole('button', { name: 'directories.details' }));
  fireEvent.click(screen.getByRole('button', { name: 'directories.exclude' }));
  await screen.findByText('Document 1');
  expect(screen.queryByText('Document 6')).not.toBeInTheDocument();
  expect(screen.getAllByRole('button', { name: 'directories.details' })).toHaveLength(5);
  expect(api.getDirectory).toHaveBeenLastCalledWith('d', expect.objectContaining({ offset: 0 }));
  expect(screen.getByTitle('Next Page')).toHaveAttribute('aria-disabled', 'true');
});

it('deletes from a directory card only after confirmation and refreshes the overview', async () => {
  vi.mocked(api.directoryAction).mockImplementation(async () => {
    vi.mocked(api.listDirectories).mockResolvedValue([]);
    return {};
  });
  render(<MemoryRouter><DirectoryPage /></MemoryRouter>);
  fireEvent.click(await screen.findByRole('button', { name: 'Contracts directories.more' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'directories.delete' }));
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).getByText('Contracts')).toBeInTheDocument();
  expect(api.directoryAction).not.toHaveBeenCalled();
  fireEvent.click(within(dialog).getByRole('button', { name: 'directories.cancel' }));
  expect(api.directoryAction).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Contracts directories.more' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'directories.delete' }));
  fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'directories.delete' }));
  await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'delete', directory_id: 'd', version: 2 }));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Contracts directories.more' })).not.toBeInTheDocument());
});

it('restores excluded documents from the header menu and offers directory deletion there', async () => {
  mockDirectory({ directory, groups: [], organizing: false,
    entries: [entry, { ...entry, id: 'removed', title: 'Removed document', excluded: true }] });
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'directories.more' }));
  expect(await screen.findByRole('menuitem', { name: 'directories.delete' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('menuitem', { name: 'directories.excluded' }));
  expect(await screen.findByText('Removed document')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'directories.restore' }));
  await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'restore', directory_id: 'd', version: 2, entry_id: 'removed' }));
});

it('edits groups beside their names and only allows deleting empty leaf groups', async () => {
  mockDirectory({ directory, organizing: false,
    groups: [{ id: 'g', name: 'Plans', parent_id: 'parent', position: 0 },
      { id: 'parent', name: 'Parent', parent_id: '', position: 1 },
      { id: 'empty', name: 'Empty', parent_id: 'parent', position: 2 }],
    entries: [{ ...entry, group_id: 'g' }] });
  const mounted = view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'Parent / Plans directories.more' }));
  expect(await screen.findByRole('menuitem', { name: 'directories.removeGroup' })).toHaveAttribute('aria-disabled', 'true');
  fireEvent.click(screen.getByRole('menuitem', { name: 'directories.editGroup' }));
  const dialog = await screen.findByRole('dialog');
  expect(within(dialog).queryByLabelText('directories.parent')).not.toBeInTheDocument();
  fireEvent.change(within(dialog).getByLabelText('directories.name'), { target: { value: 'Updated plans' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'OK' }));
  await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith(expect.objectContaining({ operation: 'group', group_id: 'g', name: 'Updated plans', parent_id: 'parent' })));
  mounted.unmount();
  const parentView = view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'Parent directories.more' }));
  expect(await screen.findByRole('menuitem', { name: 'directories.removeGroup' })).toHaveAttribute('aria-disabled', 'true');
  parentView.unmount();
  view(); await screen.findByText('Contract A');
  fireEvent.click(screen.getByRole('button', { name: 'Parent / Empty directories.more' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'directories.removeGroup' }));
  await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith(expect.objectContaining({ operation: 'delete_group', group_id: 'empty' })));
});
