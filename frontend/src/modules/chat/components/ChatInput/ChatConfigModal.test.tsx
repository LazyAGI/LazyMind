import { describe, expect, it } from 'vitest';
import type { ChatExecutorDescriptor } from '../../utils/request';
import { buildExecutorCatalog, resolveWorkflowExecutionMode } from './ChatConfigModal';

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

describe('resolveWorkflowExecutionMode', () => {
  it('derives an active workflow mode without persisting settings during hydration', () => {
    const settings = {
      enable_workflow: false,
    };

    expect(resolveWorkflowExecutionMode(settings, true)).toBe('dynamic');
    expect(settings).toEqual({ enable_workflow: false });
  });

  it('keeps the persisted disabled mode when no workflow session is active', () => {
    expect(resolveWorkflowExecutionMode({ enable_workflow: false }, false)).toBe('disabled');
  });

  it('uses the disabled effective mode when the workflow master control is unavailable', () => {
    expect(resolveWorkflowExecutionMode({ enable_workflow: true }, false, false)).toBe('disabled');
  });
});
