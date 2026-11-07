import i18n from '@/i18n';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { axiosInstance } from '@/components/request';
import StateGraphModal from './index';
import type { StateGraphData } from './StateGraphView';

vi.mock('@/components/request', () => ({ axiosInstance: { get: vi.fn() }, BASE_URL: '', localizeErrorCode: (code: string) => code }));
vi.mock('./StateGraphView', () => ({ default: ({ data }: { data: StateGraphData }) => <div data-testid='graph'>{JSON.stringify(data)}</div> }));
vi.mock('antd', () => ({ Modal: ({ children }: { children: React.ReactNode }) => <div>{children}</div>, Spin: () => <span>Loading</span> }));
beforeEach(async () => { await i18n.changeLanguage('zh-CN'); });
afterEach(cleanup);

it('keeps readiness and invalidated results as pending explanations, preserving effective attempts', async () => {
  const pending = { execution: 'none', readiness: 'not_applicable', branch: 'active', validity: 'effective' };
  const states = {
    blocked: { ...pending, readiness: 'blocked', evaluation: { missing_groups: [['outline'], ['images', 'template']] } },
    stale: { ...pending, validity: 'stale', readiness: 'ready' },
    oldInterrupted: { ...pending, execution: 'interrupted', validity: 'stale' },
    ready: { ...pending, readiness: 'ready' },
    queued: { ...pending, execution: 'pending' },
    completed: { ...pending, execution: 'succeeded' },
    running: { ...pending, execution: 'running' },
  };
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: {
    display: { status: 'paused' }, projection: { nodes: states, stale: Object.keys(states), edges: [
      { from: 'stale', to: 'blocked', state: 'stale', when: '旧分支条件' },
    ] }, graph: { static_order: Object.keys(states) },
  } });
  render(<StateGraphModal open onClose={vi.fn()} sessionId='session' workflowId='workflow' />);
  const graph: StateGraphData = JSON.parse((await screen.findByTestId('graph')).textContent!);
  const nodes = Object.fromEntries(graph.nodes.map(node => [node.id, node]));
  expect(nodes.blocked.status).toBe('pending');
  expect(nodes.blocked.status_notes).toEqual(['等待依赖：输入条件尚未满足，涉及输入：outline、images、template。']);
  for (const id of ['stale', 'oldInterrupted']) {
    expect(nodes[id].status).toBe('pending');
    expect(nodes[id].status_notes).toEqual(['原执行结果已不再适用，需要重新生成。']);
  }
  for (const [id, status] of [['queued', 'pending'], ['completed', 'succeeded'], ['running', 'running']]) {
    expect(nodes[id].status).toBe(status);
    expect(nodes[id].status_notes).toEqual([]);
  }
  expect(nodes.ready.status).toBe('pending');
  expect(nodes.ready.status_notes).toEqual(['依赖已满足，等待开始执行。']);
  expect(graph.edges[0]).toMatchObject({ edge_type: 'inactive', condition: '旧分支条件' });
});

it.each(['pruned', 'bypassed'])('shows %s only for a skipped node and preserves completed execution', async branch => {
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: { projection: { nodes: {
    prompts: { execution: 'none', readiness: 'not_applicable', branch },
    outline: { execution: 'succeeded', readiness: 'not_applicable', branch },
  } }, graph: { static_order: ['outline', 'prompts'] } } });
  render(<StateGraphModal open onClose={vi.fn()} sessionId='session' workflowId='workflow' />);
  const graph: StateGraphData = JSON.parse((await screen.findByTestId('graph')).textContent!);
  expect(graph.nodes.find(node => node.id === 'prompts')?.status).toBe('skipped');
  expect(graph.nodes.find(node => node.id === 'outline')?.status).toBe('succeeded');
});

it.each(['paused', 'failed'])('keeps graph interruption consistent with the %s workflow, including ready-to-resume nodes', async status => {
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: { display: { status }, projection: {
    current: ['search'], nodes: { search: { execution: 'interrupted', readiness: 'ready', branch: 'active' } },
  }, graph: { static_order: ['search'], nodes: { search: { label: 'Search' } } }, attempt_history: { search: [
    { attempt: 1, status: 'succeeded', duration_sec: 1, artifact_count: 0, started_at: '' },
    { attempt: 2, status: 'interrupted', duration_sec: 1, artifact_count: 0, started_at: '' },
  ] } } });

  render(<StateGraphModal open onClose={vi.fn()} sessionId='session' workflowId='workflow' />);
  const graph: StateGraphData = JSON.parse((await screen.findByTestId('graph')).textContent!);
  const node = graph.nodes.find(item => item.id === 'search')!;
  expect(node.status).toBe(status);
  expect(node.step_attempts?.[0].status).toBe('succeeded');
  expect(node.step_attempts?.[1].status).toBe(status);
});

it('keeps a replaced interrupted attempt out of the current node status', async () => {
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: { display: { status: 'running' }, projection: {
    nodes: { search: { execution: 'running', validity: 'effective', branch: 'active' } },
  }, graph: { static_order: ['search'] }, attempt_history: { search: [
    { attempt: 1, status: 'interrupted', validity: 'stale' },
    { attempt: 2, status: 'running', validity: 'effective' },
  ] } } });
  render(<StateGraphModal open onClose={vi.fn()} sessionId='session' workflowId='workflow' />);
  const graph: StateGraphData = JSON.parse((await screen.findByTestId('graph')).textContent!);
  expect(graph.nodes.find(node => node.id === 'search')?.status).toBe('running');
});


it('translates pending explanations and input lists in English', async () => {
  await i18n.changeLanguage('en-US');
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: {
    projection: { nodes: {
      blocked: { execution: 'none', readiness: 'blocked', branch: 'active', evaluation: { missing_groups: [['outline', 'images']] } },
      ready: { execution: 'none', readiness: 'ready', branch: 'active' },
      stale: { execution: 'none', readiness: 'ready', branch: 'active', validity: 'stale' },
    } }, graph: { static_order: ['blocked', 'ready', 'stale'] },
  } });
  render(<StateGraphModal open onClose={vi.fn()} sessionId='session' workflowId='workflow' />);
  const graph: StateGraphData = JSON.parse((await screen.findByTestId('graph')).textContent!);
  const notes = graph.nodes.flatMap(node => node.status_notes ?? []);
  expect(notes).toEqual([
    'Waiting for dependencies: input requirements are not met. Required inputs: outline, images.',
    'Dependencies are satisfied. Waiting to start.',
    'The previous result is no longer applicable and needs to be regenerated.',
  ]);
});

it('shows the input checkpoint as waiting while keeping its successful attempt history', async () => {
  vi.mocked(axiosInstance.get).mockResolvedValue({ data: {
    display: { status: 'waiting_input', input_step_id: 'collect' },
    projection: { nodes: { collect: { execution: 'succeeded', validity: 'effective', branch: 'active' } } },
    graph: { static_order: ['collect'] },
    attempt_history: { collect: [{ attempt: 1, status: 'succeeded', validity: 'effective' }] },
  } });
  render(<StateGraphModal open onClose={vi.fn()} sessionId='session' workflowId='workflow' />);
  const graph: StateGraphData = JSON.parse((await screen.findByTestId('graph')).textContent!);
  expect(graph.nodes.find(node => node.id === 'collect')).toMatchObject({
    status: 'waiting_input', is_current: true, step_attempts: [{ attempt: 1, status: 'succeeded' }],
  });
});
