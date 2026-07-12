import { describe, expect, it } from 'vitest';
import {
  buildLocalPathChildrenRequest,
  buildLocalPathInitialChildrenRequest,
} from '../../frontend/src/modules/dataSource/utils/localPathTreeContract';

describe('local path tree request contract', () => {
  it('uses an empty target ref to list initial mounted roots', () => {
    expect(buildLocalPathInitialChildrenRequest('agent-1')).toEqual({
      connector_type: 'local_fs',
      target_type: 'local_path',
      target_ref: '',
      agent_id: 'agent-1',
      include_files: false,
      list_mode: 'page',
      page_size: 50,
    });
  });

  it('preserves the selected root ref when expanding its children', () => {
    expect(
      buildLocalPathChildrenRequest({
        targetRef: '/',
        nodeRef: 'local-root-node',
        agentId: 'agent-1',
      }),
    ).toMatchObject({
      target_ref: '/',
      node_ref: 'local-root-node',
      agent_id: 'agent-1',
    });
  });

  it('does not invent a node ref when a connector only returns target_ref', () => {
    expect(
      buildLocalPathChildrenRequest({ targetRef: '/Documents/LazyMind' }),
    ).toMatchObject({
      target_ref: '/Documents/LazyMind',
      node_ref: undefined,
    });
  });
});
