import { act, cleanup, fireEvent, render, screen, within, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ConfigProvider } from 'antd';
import DirectoryPage from './DirectoryPage';
import * as api from './api';
const t = (key: string) => key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t }) }));
vi.mock('@/components/request', () => ({ getLocalizedErrorMessage: () => 'Read failed' }));
vi.mock('./api', () => ({ listDirectories: vi.fn(), getDirectory: vi.fn(), listBatches: vi.fn(), directoryAction: vi.fn(), readDirectoryMail: vi.fn(), readDirectoryLocalFile: vi.fn() }));
vi.mock('./LocalFileReader', () => ({ default: () => null }));
const directory = { id: 'A', name: 'Directory A', intent: 'Intent A', organization: 'Type', pinned: false, auto_add: true, version: 2, last_used_at: '2026-10-19T00:00:00Z', coverage: [] };
const viewData = (id = 'A') => ({ directory: id === 'A' ? directory : { ...directory, id: 'B', name: 'Directory B', intent: 'Intent B', version: 99 }, entries: [], groups: [], organizing: false, group_counts: {}, total: 0 });
function Nav() { const navigate = useNavigate(); const location = useLocation(); return <><button onClick={() => navigate('/document-directories/B')}>Go B</button><output data-testid="route">{location.pathname}</output></>; }
function view() { return render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter initialEntries={['/document-directories/A']}><Nav /><Routes><Route path="/document-directories/:directoryId" element={<DirectoryPage />} /></Routes></MemoryRouter></ConfigProvider>); }
beforeEach(() => {
 vi.clearAllMocks();
 vi.mocked(api.listDirectories).mockResolvedValue([directory]);
 vi.mocked(api.listBatches).mockResolvedValue([]);
 vi.mocked(api.getDirectory).mockImplementation(async id => viewData(id));
 vi.mocked(api.directoryAction).mockResolvedValue({});
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

it.each([1000, 6000])('accepts successful refreshes with %d ms latency', async latency => {
 vi.useFakeTimers();
 let completed = 0;
 vi.mocked(api.getDirectory).mockImplementation(id => new Promise(resolve => setTimeout(() => { completed++; resolve(viewData(id)); }, latency)));
 view();
 for (let elapsed=0; elapsed<26000; elapsed+=1000) await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
 const visible = screen.queryByRole('heading', { name: 'Directory A' }) !== null;
 expect(visible).toBe(true);
});

it('keeps B visible when an earlier mutation in A completes', async () => {
 let release!: (value: object) => void;
 const pending = new Promise<object>(resolve => { release = resolve; });
 vi.mocked(api.directoryAction).mockImplementation(command => command.operation === 'update' ? pending : Promise.resolve({}));
 view();
 await screen.findByRole('heading', {name:'Directory A'});
 fireEvent.click(screen.getByRole('button', {name:'directories.directoryPin'}));
 fireEvent.click(screen.getByRole('button', {name:'Go B'}));
 await screen.findByRole('heading', {name:'Directory B'});
 await act(async () => { release({}); await pending; });
 const heading = screen.getByRole('heading').textContent;
 expect(heading).toBe('Directory B');
});

it('acknowledges a successful result after opening its directory', async () => {
 vi.mocked(api.listBatches).mockResolvedValue([{id:'done',directory_id:'A',parent_id:'',status:'succeeded',acknowledged:false,created_at:directory.last_used_at}]);
 view();
 await screen.findByRole('heading',{name:'Directory A'});
 await waitFor(() => expect(api.directoryAction).toHaveBeenCalledWith({ operation: 'acknowledge', batch_id: 'done' }));
});


it('does not close B editor after A mutation refresh completes', async () => {
 let release!: (value: ReturnType<typeof viewData>) => void;
 const pending = new Promise<ReturnType<typeof viewData>>(resolve => { release = resolve; });
 view();
 await screen.findByRole('heading', { name: 'Directory A' });
 fireEvent.click(screen.getByRole('button', { name: 'directories.more' }));
 fireEvent.click(await screen.findByRole('menuitem', { name: 'directories.edit' }));
 const dialog = await screen.findByRole('dialog');
 let updating = false;
 let refreshStarted = false;
 vi.mocked(api.directoryAction).mockImplementation(async command => { if (command.operation === 'update') updating = true; return {}; });
 vi.mocked(api.getDirectory).mockImplementation(id => {
   if (id === 'A' && updating) { refreshStarted = true; return pending; }
   return Promise.resolve(viewData(id));
 });
 fireEvent.click(within(dialog).getByRole('button', { name: 'OK' }));
 await waitFor(() => expect(refreshStarted).toBe(true), { timeout: 10000 });
 fireEvent.click(screen.getByRole('button', { name: 'Go B' }));
 await screen.findByRole('heading', { name: 'Directory B' });
 fireEvent.click(screen.getByRole('button', { name: 'directories.more' }));
 fireEvent.click(await screen.findByRole('menuitem', { name: 'directories.edit' }));
 await screen.findByDisplayValue('Directory B');
 await waitFor(() => expect(screen.getByRole('dialog')).toBeVisible(), { timeout: 10000 });
 vi.useFakeTimers();
 try {
   await act(async () => { release(viewData('A')); await pending; });
   // Advance the closing animation so a stale completion cannot hide B's editor later.
   await act(async () => { await vi.advanceTimersByTimeAsync(400); });
   expect(screen.getByRole('dialog')).toBeVisible();
   expect(screen.getByDisplayValue('Directory B')).toBeVisible();
 } finally {
   // Unmount under the real clock to clear polling timers created before this scope.
   vi.useRealTimers();
 }
}, 20000);
