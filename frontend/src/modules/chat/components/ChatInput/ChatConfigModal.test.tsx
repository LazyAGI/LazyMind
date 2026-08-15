import { describe, expect, it } from 'vitest';
import type { ChatExecutorDescriptor } from '../../utils/request';
import { buildExecutorCatalog } from './ChatConfigModal';

describe('buildExecutorCatalog', () => {
  it('always keeps LazyMind selectable when the external catalog is unavailable', () => {
    const catalog = buildExecutorCatalog([], 'codex', 'host unavailable');

    expect(catalog).toEqual([
      expect.objectContaining({ id: 'lazymind', available: true }),
      expect.objectContaining({ id: 'codex', available: false }),
    ]);
  });

  it('uses the live catalog for connected external executors', () => {
    const externalExecutors: ChatExecutorDescriptor[] = [
      {
        id: 'codex',
        display_name: 'Codex',
        kind: 'external',
        installed: true,
        host_online: true,
        available: true,
      },
      {
        id: 'cursor',
        display_name: 'Cursor',
        kind: 'external',
        installed: true,
        host_online: true,
        available: true,
      },
    ];

    expect(buildExecutorCatalog(externalExecutors, 'codex', 'unavailable')).toEqual([
      expect.objectContaining({ id: 'lazymind', available: true }),
      expect.objectContaining({ id: 'codex', available: true }),
      expect.objectContaining({ id: 'cursor', available: true }),
    ]);
  });
});
