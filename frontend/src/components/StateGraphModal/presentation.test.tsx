import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import StateGraphModal from './index';

const fixture = vi.hoisted(() => ({ get: vi.fn(), loadUI: vi.fn() }));
vi.mock('@/components/request', async original => {
  const actual = await original<typeof import('@/components/request')>();
  return { ...actual, axiosInstance: { ...actual.axiosInstance, get: fixture.get } };
});
vi.mock('@/modules/chat/store/workflowPanel', async original => {
  const actual = await original<typeof import('@/modules/chat/store/workflowPanel')>();
  return { ...actual, loadWorkflowUI: fixture.loadUI };
});
const graph = { graph: { static_order: ['generate_ppt'], nodes: { generate_ppt: { label: 'Generate PPT' } } },
  projection: { nodes: { generate_ppt: { execution: 'running', validity: 'effective' } }, current: ['generate_ppt'], edges: [] } };
afterEach(cleanup);
beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  fixture.get.mockReset().mockImplementation(async (url: string) => ({ data: { data: url.endsWith('/projection')
    ? graph : { session: { workflow_id: 'ppt-workflow', pinned_revision_id: 'pinned' } } } }));
  fixture.loadUI.mockReset().mockResolvedValue({ stepLabels: { generate_ppt: '生成PPT' }, tabs: [{ id: 'slides', step_id: 'generate_ppt', label: '幻灯片' }] });
});

it('shares the same localized data when moving from inline graph to modal', async () => {
  const close = vi.fn();
  const props = { open: true, sessionId: 'run', workflowId: 'ppt-workflow', onClose: close, stepLabels: { generate_ppt: '幻灯片' } };
  const view = render(<StateGraphModal {...props} inline />);
  expect(await screen.findByText('幻灯片')).toBeVisible();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  view.rerender(<StateGraphModal {...props} />);
  expect(await screen.findByRole('dialog')).toHaveTextContent('幻灯片');
  expect(fixture.get).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: /Close/ }));
  expect(close).toHaveBeenCalledOnce();
});

it('uses pinned localized panel labels when opened from the task center', async () => {
  render(<StateGraphModal open inline sessionId='run' workflowId='' onClose={vi.fn()} />);
  expect(await screen.findByText('幻灯片')).toBeVisible();
  expect(screen.queryByText('Generate PPT')).not.toBeInTheDocument();
  await waitFor(() => expect(fixture.loadUI).toHaveBeenCalledWith('ppt-workflow', 'pinned', { language: 'zh-CN', silentError: true }));
});

it.each(['detail', 'labels'])('keeps the authoritative graph and reports a %s lookup failure', async failure => {
  if (failure === 'labels') fixture.loadUI.mockRejectedValue(new Error('labels unavailable'));
  else fixture.get.mockImplementation(async (url: string) => {
    if (!url.endsWith('/projection')) throw new Error('session unavailable');
    return { data: { data: graph } };
  });
  render(<StateGraphModal open inline sessionId='run' workflowId='' onClose={vi.fn()}
    fallbackSteps={[{ step_id: 'stale fallback', status: 'pending' }]} />);
  expect(await screen.findByText('Generate PPT')).toBeVisible();
  expect(screen.queryByText('stale fallback')).not.toBeInTheDocument();
  expect(screen.getByText('阶段名称加载失败，当前显示工作流原始名称。执行状态不受影响。')).toBeVisible();
});
