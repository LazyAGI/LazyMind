import { webcrypto } from 'node:crypto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { emptyLibraryV3, migratePaperLibrary, type PaperLibraryDataV3 } from './libraryModel';
import { sampleLibrary } from './model';
import { loadPersistedPaperLibrary, savePersistedPaperLibrary, savePreviewPaperLibrary } from './store';

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: api }));

let server: { data: PaperLibraryDataV3; revision: number; can_write: boolean };
const clone = <T,>(value: T): T => JSON.parse(JSON.stringify(value));
beforeEach(() => {
  vi.stubGlobal('crypto', webcrypto);
  localStorage.clear();
  server = { data: emptyLibraryV3(), revision: 0, can_write: true };
  api.get.mockReset().mockImplementation(async () => ({ data: clone(server) }));
  api.put.mockReset().mockImplementation(async (_url, body) => {
    if (body.revision !== server.revision) throw { response: { status: 409, data: { message: '文献库已被其他用户更新，请刷新后重试' } } };
    server = { data: clone(body.data), revision: server.revision + 1, can_write: true };
    return { data: clone(server) };
  });
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear(); });

describe('server-owned paper library', () => {
  it('retains the complete graph after browser storage is cleared', async () => {
    const data = migratePaperLibrary(sampleLibrary());
    data.notes.push({ id: 'n', paperId: data.papers[0].id, title: 'Note', content: 'Durable research', kind: 'note', createdAt: '2026-10-21', updatedAt: '2026-10-21' });
    data.papers[0].noteIds = ['n'];
    data.papers[0].tags = ['server-tag'];
    data.papers[0].important = true;
    data.papers[0].status = 'read';
    const firstClient = await loadPersistedPaperLibrary('shared');
    await savePersistedPaperLibrary('shared', data, firstClient.revision);
    localStorage.clear();
    const secondClient = await loadPersistedPaperLibrary('shared');
    expect(secondClient.data).toEqual(data);
    expect(secondClient.revision).toBe(1);
    expect(localStorage.length).toBe(0);
  });

  it('rejects stale clients without overwriting a newer edit', async () => {
    const clientA = await loadPersistedPaperLibrary('shared');
    const clientB = await loadPersistedPaperLibrary('shared');
    const saved = { ...clientA.data, folders: [{ id: 'a', name: 'Client A' }] };
    await savePersistedPaperLibrary('shared', saved, clientA.revision);
    await expect(savePersistedPaperLibrary('shared', { ...clientB.data, folders: [{ id: 'b', name: 'Client B' }] }, clientB.revision)).rejects.toThrow('其他用户');
    expect((await loadPersistedPaperLibrary('shared')).data).toEqual(saved);
  });

  it('does not substitute a local save when the backend rejects the write', async () => {
    api.put.mockRejectedValue({ response: { status: 500, data: { message: '数据库写入失败' } } });
    await expect(savePersistedPaperLibrary('shared', migratePaperLibrary(sampleLibrary()), 0)).rejects.toThrow('数据库写入失败');
    expect(localStorage.length).toBe(0);
    expect(server.data.papers).toEqual([]);
  });

  it('does not use the old browser copy when the server read fails', async () => {
    savePreviewPaperLibrary('shared', migratePaperLibrary(sampleLibrary()));
    api.get.mockRejectedValue({ response: { status: 503, data: { message: '数据库不可用' } } });
    await expect(loadPersistedPaperLibrary('shared')).rejects.toThrow('数据库不可用');
    expect(api.put).not.toHaveBeenCalled();
  });

  it('works with browser storage unavailable when no legacy copy can be read', async () => {
    vi.spyOn(localStorage, 'getItem').mockImplementation(() => { throw new Error('Blocked'); });
    expect((await loadPersistedPaperLibrary('shared')).revision).toBe(0);
    await savePersistedPaperLibrary('shared', migratePaperLibrary(sampleLibrary()), 0);
    expect(server.data.papers.length).toBeGreaterThan(0);
  });

  it('reads only the server when a browser preview exists', async () => {
    savePreviewPaperLibrary('shared', migratePaperLibrary(sampleLibrary()));
    expect((await loadPersistedPaperLibrary('shared')).data).toEqual(emptyLibraryV3());
    expect(api.put).not.toHaveBeenCalled();
  });

  it('reports corrupt server data instead of treating it as an empty library', async () => {
    api.get.mockResolvedValue({ data: { data: { schemaVersion: 3 }, revision: 1, can_write: true } });
    await expect(loadPersistedPaperLibrary('shared')).rejects.toThrow('格式');
  });

  it('exposes read-only permission and never writes an empty library on read', async () => {
    server.can_write = false;
    expect((await loadPersistedPaperLibrary('shared')).canWrite).toBe(false);
    expect(api.put).not.toHaveBeenCalled();
  });
});
