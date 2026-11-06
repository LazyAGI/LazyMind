import i18n from '@/i18n';
import { useState } from 'react';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import StateGraphView, { type SGNode, type StateGraphData } from './StateGraphView';

vi.mock('antd', () => ({
  Popover: ({ children, content }: { children: React.ReactNode; content: React.ReactNode }) => {
    const [open, setOpen] = useState(false);
    return <><button onClick={() => setOpen(!open)}>{children}</button>{open && <div role='dialog'>{content}</div>}</>;
  },
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
beforeEach(async () => { await i18n.changeLanguage('zh-CN'); });
afterEach(cleanup);
const node = (id: string, status: string, extra: Partial<SGNode> = {}): SGNode => ({
  id, label: id, status, step_index: 1, is_current: false, ...extra,
});

it('deduplicates runtime aliases and only describes visible node/edge styles', () => {
  const nodes = [node('__start__', 'succeeded'), node('a', 'interrupted'), node('b', 'paused'),
    node('c', 'skipped'), node('d', 'pruned'), node('e', 'bypassed'), node('f', 'running')];
  render(<StateGraphView data={{ nodes, initial: '__start__', edges: [
    { from: 'b', to: 'c', condition: '', edge_type: 'pruned' },
    { from: 'c', to: 'd', condition: '', edge_type: 'bypassed' },
    { from: 'missing', to: 'e', condition: '', edge_type: 'inactive' },
  ] }} />);
  const legend = within(screen.getByLabelText('工作流图例'));
  for (const label of ['已失败', '已暂停', '已跳过', '执行中', '已跳过路线']) expect(legend.getAllByText(label)).toHaveLength(1);
  for (const label of ['已中断', '运行中', '已完成', '可执行', '输入未满足', '结果已失效', '后续可达', '已失效路线']) {
    expect(legend.queryByText(label)).not.toBeInTheDocument();
  }
});

it('updates node status and edge styling without changing topology', () => {
  const data: StateGraphData = { initial: 'a', nodes: [node('a', 'running'), node('b', 'pending')], edges: [
    { from: 'a', to: 'b', condition: '', edge_type: 'inactive' },
  ] };
  const { rerender, container } = render(<StateGraphView data={data} />);
  rerender(<StateGraphView data={{ ...data, nodes: [node('a', 'succeeded'), node('b', 'interrupted', { is_current: true })], edges: [
    { ...data.edges[0], edge_type: 'current_direct' },
  ] }} />);
  const graph = within(screen.getByLabelText('Workflow graph'));
  expect(graph.queryByText('执行中')).not.toBeInTheDocument();
  expect(graph.getByText('已完成')).toBeInTheDocument();
  expect(graph.getByText('已失败')).toBeInTheDocument();
  const edge = container.querySelector('path[marker-end]')!;
  expect(edge.getAttribute('stroke')).toBe('#1677ff');
  expect(edge.getAttribute('stroke-dasharray')).toBeNull();
});

it('matches every edge legend to the rendered line and arrowhead', () => {
  const types = ['executed', 'current_direct', 'pruned', 'bypassed', 'inactive'] as const;
  const statuses = ['succeeded', 'ready', 'pending', 'canceled', 'pending', 'failed'];
  const { container } = render(<StateGraphView data={{ initial: '0', nodes: statuses.map((s, i) => node(String(i), s)),
    edges: types.map((edge_type, i) => ({ from: String(i), to: String(i + 1), condition: '', edge_type })),
  }} />);
  const legend = screen.getByLabelText('工作流图例');
  expect(within(legend).getAllByText('待执行')).toHaveLength(1);
  expect(within(legend).queryByText('可执行')).not.toBeInTheDocument();
  for (const label of ['输入未满足', '结果已失效', '已失效路线']) expect(within(legend).queryByText(label)).not.toBeInTheDocument();
  for (const edge of container.querySelectorAll('path[marker-end]')) {
    const stroke = edge.getAttribute('stroke');
    const dash = edge.getAttribute('stroke-dasharray');
    expect([...legend.querySelectorAll('line')].some(line => line.getAttribute('stroke') === stroke && line.getAttribute('stroke-dasharray') === dash)).toBe(true);
    const markerId = edge.getAttribute('marker-end')!.slice(5, -1);
    const marker = [...container.querySelectorAll('marker')].find(item => item.id === markerId)!;
    expect(marker.querySelector('path')?.getAttribute('fill')).toBe(stroke);
  }
});

it('shows dependency and regeneration explanations only in node details', () => {
  const notes = ['原执行结果已不再适用，需要重新生成。', '等待依赖：输入条件尚未满足，涉及输入：大纲。'];
  render(<StateGraphView data={{ initial: 'step', nodes: [node('等待步骤', 'pending', { status_notes: notes })], edges: [] }} />);
  expect(within(screen.getByLabelText('工作流图例')).getByText('待执行')).toBeInTheDocument();
  expect(screen.queryByText(notes[0])).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: /等待步骤/ }));
  for (const note of notes) expect(within(screen.getByRole('dialog')).getByText(note)).toBeInTheDocument();
});


it('updates status and route hints when switching between Chinese and English', async () => {
  const nodes = [node('a', 'paused'), node('b', 'pending'), node('c', 'skipped')];
  render(<StateGraphView data={{ nodes, initial: 'a', edges: [
    { from: 'a', to: 'b', condition: '', edge_type: 'current_direct' },
    { from: 'b', to: 'c', condition: '', edge_type: 'bypassed' },
  ] }} />);
  expect(within(screen.getByLabelText('工作流图例')).getByText('已暂停')).toBeInTheDocument();
  await act(async () => { await i18n.changeLanguage('en-US'); });
  const legend = within(screen.getByLabelText('Workflow legend'));
  for (const label of ['Paused', 'Pending', 'Skipped', 'Active route', 'Skipped route']) {
    expect(legend.getByText(label)).toBeInTheDocument();
  }
  expect(screen.queryByText('已暂停')).not.toBeInTheDocument();
});
