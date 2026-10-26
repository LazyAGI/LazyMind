import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Dataset } from '@/api/generated/knowledge-client';
import { useDatasetPermissionStore } from './dataset_permission';

const api = vi.hoisted(() => ({ getDataset: vi.fn() }));
vi.mock('../utils/request', () => ({ KnowledgeBaseServiceApi: () => ({ datasetServiceGetDataset: api.getDataset }) }));
const dataset = (id: string): Dataset => ({ dataset_id: id, library_kind: 'paper', type: 'DATASET_TYPE_TEXT' });

beforeEach(() => {
  useDatasetPermissionStore.getState().clearDataset();
  api.getDataset.mockReset();
});

describe('shared knowledge base details', () => {
  it('shares an in-flight request and reuses its result across the layout and pages', async () => {
    api.getDataset.mockResolvedValue({ data: dataset('papers') });
    const store = useDatasetPermissionStore.getState();
    const layout = store.loadDataset('papers');
    const page = store.loadDataset('papers');
    expect(page).toBe(layout);
    await Promise.all([layout, page]);
    expect(await store.loadDataset('papers')).toEqual(dataset('papers'));
    expect(api.getDataset).toHaveBeenCalledTimes(1);
    api.getDataset.mockResolvedValue({ data: { ...dataset('papers'), display_name: 'Updated' } });
    await store.loadDataset('papers', true);
    expect(useDatasetPermissionStore.getState().currentDataset?.display_name).toBe('Updated');
  });

  it('does not replace the new knowledge base with a late response from the previous one', async () => {
    let finishOld!: (value: { data: Dataset }) => void;
    api.getDataset.mockReturnValueOnce(new Promise(resolve => { finishOld = resolve; })).mockResolvedValueOnce({ data: dataset('new') });
    const store = useDatasetPermissionStore.getState();
    const old = store.loadDataset('old');
    await store.loadDataset('new');
    finishOld({ data: dataset('old') });
    await old;
    expect(useDatasetPermissionStore.getState().currentDataset?.dataset_id).toBe('new');
  });

  it('does not restore cleared details when an abandoned request finishes', async () => {
    let finish!: (value: { data: Dataset }) => void;
    api.getDataset.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    const store = useDatasetPermissionStore.getState();
    const request = store.loadDataset('papers');
    store.clearDataset();
    finish({ data: dataset('papers') });
    await request;
    expect(useDatasetPermissionStore.getState().currentDataset).toBeNull();
  });

  it('records failures and can retry without retaining permissions from a different base', async () => {
    const store = useDatasetPermissionStore.getState();
    store.setCurrentDataset({ ...dataset('old'), acl: ['DATASET_WRITE'] });
    api.getDataset.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ data: dataset('papers') });
    await expect(store.loadDataset('papers')).rejects.toThrow('offline');
    expect(useDatasetPermissionStore.getState().currentDataset).toBeNull();
    expect(useDatasetPermissionStore.getState().datasetError).toBe('offline');
    await store.loadDataset('papers');
    expect(useDatasetPermissionStore.getState().datasetError).toBe('');
    expect(useDatasetPermissionStore.getState().currentDataset?.dataset_id).toBe('papers');
  });
});
