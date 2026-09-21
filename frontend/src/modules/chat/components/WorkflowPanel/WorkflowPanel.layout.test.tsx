import React, { useContext, useEffect } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { WorkflowSession, WorkflowUI } from '@/modules/chat/store/workflowPanel';
import { SlotEditingContext, WorkflowPanelTabActiveContext } from './slotEditingContext';
import { WorkflowPanel } from './index';
import { controlActions, type WorkflowControlView } from '@/modules/chat/utils/workflowControl';
import { loadWorkflowRunSnapshot } from '@/modules/chat/utils/loadWorkflowRun';

const fixture = vi.hoisted(() => ({
  session: {} as WorkflowSession,
  ui: {} as WorkflowUI,
  flush: vi.fn(async () => true),
  refresh: vi.fn(),
  setApprovalPreference: vi.fn(async () => {}),
  setFocusedTab: vi.fn(),
}));
vi.mock('@/modules/chat/hooks/useWorkflow', () => ({
  useWorkflowSession: () => ({ session: fixture.session, loading: false, refresh: fixture.refresh }),
}));
vi.mock('@/modules/chat/store/workflowPanel', async (original) => {
  const actual = await original<typeof import('@/modules/chat/store/workflowPanel')>();
  const state = {
    get sessionByConversation() { return { 'layout-test': fixture.session }; },
    bumpDismissedRefresh: vi.fn(), autoRunningByConversation: {}, setAutoRunning: vi.fn(),
    fetchWorkflowUI: () => Promise.resolve(fixture.ui), setFocusedTab: fixture.setFocusedTab,
    setFocusedSortOrder: vi.fn(), focusedTabByConversation: {}, workflowUIByWorkflow: {},
  };
  return { ...actual, useWorkflowStore: Object.assign((selector?: (value: typeof state) => unknown) => selector ? selector(state) : state, { getState: () => state }) };
});
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => ({ setApprovalPreference: fixture.setApprovalPreference }) }));
vi.mock('@/components/StateGraphModal', () => ({ default: () => null }));
vi.mock('./actions/WorkflowTabActions', () => ({ WorkflowTabActions: () => null }));
vi.mock('./ppt/SlideThumb', () => ({ SlideThumb: () => null }));
vi.mock('./SlotComponents', () => ({
  isWriterIrSource: () => false,
  SlotDownloadContext: React.createContext(true),
  SlotMarkdownStream: () => null,
  SlotRenderer: ({ slotId, slot, readOnly, widget }: { widget?: { widgetType?: string }; slotId: string; slot: { artifact_value?: { text?: string } }; readOnly?: boolean }) => {
    const context = useContext(SlotEditingContext);
    const active = useContext(WorkflowPanelTabActiveContext);
    useEffect(() => {
      if (!active) return;
      const unregisterFlush = context.registerFlush(slotId, fixture.flush);
      const unregisterAction = context.registerFooterAction(slotId, {
        label: '发布', onClick: vi.fn(), statusText: '已写回云文档', statusTone: 'success',
        statusLink: { href: 'https://example.com/document', label: '打开云文档' },
      });
      return () => { unregisterFlush(); unregisterAction(); };
    }, [active, context.registerFlush, context.registerFooterAction, slotId]);
    return <article onDoubleClick={() => context.setEditing(slotId, true)} data-widget={widget?.widgetType} data-readonly={Boolean(readOnly)} data-value={slot.artifact_value?.text}>正文 {slotId}</article>;
  },
}));

beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  vi.clearAllMocks();
  fixture.flush.mockResolvedValue(true);
  localStorage.clear();
  fixture.ui = { name: '示例工作流', tabs: [
    { id: 'prepare', step_id: 'prepare', label: '写作准备', layout: 'list', slots: [] },
    { id: 'draft', step_id: 'write_document', label: '成稿', layout: 'list', slots: [
      { id: 'document', label: '成稿', type: 'text', widget: { widgetType: 'writer-document' } },
    ] },
  ] };
  fixture.session = {
    session_id: 'layout-test', conversation_id: 'layout-test', workflow_id: 'fixture', workflow_mode: 'auto',
    status: 'completed', current_step_id: 'write_document', created_at: '', updated_at: '',
    projection: { past: ['prepare', 'write_document'] },
    steps: ['prepare', 'write_document', 'stale_step'].map(step_id => ({
      id: step_id, session_id: 'layout-test', step_id, attempt: 1, task_id: '', status: 'succeeded',
      validity: step_id === 'stale_step' ? 'stale' : 'effective', created_at: '2026-09-16T00:00:00Z', updated_at: '',
    })),
    slots: [{ slot_id: 'document', slot: 'document', step_id: 'write_document', selected: true, revision: 1, artifact_value: { text: '正文' } }] as WorkflowSession['slots'],
  };
});
afterEach(cleanup);

describe('shared workflow compact layout', () => {
  it.each(['active', 'waiting', 'failed', 'completed'] as const)(
    'shows the full trust notice for %s sessions and after remount', async (status) => {
      fixture.session.status = status;
      fixture.session.steps = [];
      const notice = '此工作流以完全信任模式运行，可在服务进程的系统权限范围内读取、修改文件和执行代码，不受 Workspace 权限限制，无需逐次审批。';
      const view = render(<WorkflowPanel conversationId='layout-test' />);
      expect(await screen.findByText(notice)).toBeVisible();
      view.unmount();
      render(<WorkflowPanel conversationId='layout-test' />);
      expect(await screen.findByText(notice)).toBeVisible();
    },
  );

  it('keeps rerun shortcuts visible in the footer and still saves before rerunning', async () => {
    const send = vi.fn();
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    await screen.findByRole('button', { name: '发布' });
    const shortcuts = screen.getByRole('group', { name: '回退到步骤：' });
    expect(shortcuts).toBeVisible();
    expect(within(shortcuts).getByRole('button', { name: '写作准备' })).toBeVisible();
    fireEvent.click(within(shortcuts).getByRole('button', { name: '成稿' }));
    await waitFor(() => expect(send).toHaveBeenCalledWith('请重新执行步骤 write_document'));
    expect(fixture.flush).toHaveBeenCalledOnce();
    expect(within(shortcuts).queryByRole('button', { name: 'stale_step' })).not.toBeInTheDocument();
  });

  it('does not rerun when saving the current edits fails', async () => {
    fixture.flush.mockResolvedValue(false);
    const send = vi.fn();
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    await screen.findByRole('button', { name: '发布' });
    fireEvent.click(within(screen.getByRole('group', { name: '回退到步骤：' })).getByRole('button', { name: '成稿' }));
    await waitFor(() => expect(fixture.flush).toHaveBeenCalledOnce());
    expect(send).not.toHaveBeenCalled();
  });

  it('keeps shortcuts available for completed workflows without document footer actions', async () => {
    fixture.session.slots = [];
    render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿' });
    const footer = screen.getByRole('group', { name: '工作流会话操作' });
    expect(within(footer).getByRole('button', { name: '写作准备' })).toBeVisible();
    expect(within(footer).getByRole('button', { name: '成稿' })).toBeVisible();
  });

  it('opens user intent from the menu and returns focus when dismissed with Escape', async () => {
    fixture.session.intent_context = JSON.stringify({ text: '保留原文和图片' });
    render(<WorkflowPanel conversationId='layout-test' />);
    const trigger = screen.getByRole('button', { name: '工作流操作' });
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole('menuitem', { name: /用户意图/ }));
    const dialog = screen.getByRole('dialog', { name: '用户意图' });
    expect(dialog).toHaveTextContent('保留原文和图片');
    fireEvent.keyDown(within(dialog).getByRole('button', { name: '关闭' }), { key: 'Escape' });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it('preserves paged composite previews and auxiliary slots when switching pages', async () => {
    fixture.ui.tabs = [{
      id: 'slides', label: '幻灯片', layout: 'composite', slot_scope: 'selected', composite_tab_position: 'left',
      slots: [{ id: 'preview', label: '预览', type: 'file' }, { id: 'notes', label: '演讲备注', type: 'text' }],
      composite_layout: { direction: 'row', children: [{ slot: 'preview', weight: 2 }, { slot: 'notes', weight: 1 }] },
    }];
    fixture.session.slots = [0, 1].flatMap(sort_order => ['preview', 'notes'].map(slot => ({
      slot, slot_id: slot, list_index: sort_order, sort_order, selected: true, artifact_value: { text: `${slot}-${sort_order}` },
    }))) as WorkflowSession['slots'];
    render(<WorkflowPanel conversationId='layout-test' />);
    const first = await screen.findByRole('listitem', { name: '第 1 行' });
    expect(first).toHaveAttribute('aria-current', 'true');
    fireEvent.click(screen.getByRole('button', { name: '下一张幻灯片' }));
    expect(screen.getByRole('listitem', { name: '第 2 行' })).toHaveAttribute('aria-current', 'true');
    expect(screen.getByText('正文 preview')).toBeVisible();
    expect(screen.getByText('正文 notes')).toBeVisible();
  });

  it('keeps long step navigation usable with the keyboard and preserves multiple slot headings', async () => {
    fixture.ui.tabs = Array.from({ length: 7 }, (_, i) => ({ id: `step${i}`, label: `阶段 ${i + 1}`, layout: 'list', slots: [] }));
    fixture.ui.tabs[6].slots = [
      { id: 'main', label: '主文档', type: 'text', widget: { widgetType: 'writer-document' } },
      { id: 'evidence', label: '证据清单', type: 'text' },
    ];
    render(<WorkflowPanel conversationId='layout-test' />);
    const first = await screen.findByRole('tab', { name: '阶段 1' });
    first.focus();
    fireEvent.keyDown(first, { key: 'End' });
    const last = screen.getByRole('tab', { name: '阶段 7' });
    expect(last).toHaveFocus();
    expect(last).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('主文档')).toBeVisible();
    expect(screen.getByText('证据清单')).toBeVisible();
    fireEvent.keyDown(last, { key: 'ArrowRight' });
    expect(first).toHaveFocus();
  });

  it('retains only the relevant running and failed workflow controls', async () => {
    const stop = vi.fn();
    fixture.session.status = 'active';
    const view = render(<WorkflowPanel conversationId='layout-test' onStop={stop} />);
    await screen.findByRole('tab', { name: /成稿/ });
    expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '继续' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '停止' }));
    expect(stop).toHaveBeenCalledOnce();
    fixture.session = { ...fixture.session, status: 'failed' };
    view.rerender(<WorkflowPanel conversationId='layout-test' onStop={stop} />);
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled();
  });

  it.each([
    ['chat.workflowContinueExecution', undefined],
    ['chat.workflowSkipThisApproval', 'step'],
    ['chat.workflowSkipFollowingApprovals', 'following'],
  ] as const)('keeps shared approval choice %s on the native path', async (label, scope) => {
    fixture.session.status = 'waiting';
    // The approval resolver uses the latest effective attempt, not array order.
    fixture.session.steps!.find(step => step.step_id === 'write_document')!.created_at = '2026-09-16T00:01:00Z';
    fixture.session.projection!.nodes = { write_document: {
      execution: 'succeeded', requires_approval: true, validity: 'effective', reachability: '', readiness: '', branch: '',
    } };
    const send = vi.fn();
    const view = render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    await screen.findByText('正文 document');
    const approval = within(view.container.querySelector('.workflow-panel') as HTMLElement).getByRole('group', { name: '工作流审批操作' });
    expect(within(approval).getByRole('button', { name: '继续执行' })).toBeEnabled();
    const scopeButton = within(approval).getByRole('button', { name: String(i18n.t(label)) });
    await act(async () => fireEvent.click(scopeButton));
    if (scope) {
      expect(fixture.setApprovalPreference).toHaveBeenCalledWith('layout-test', { step_id: 'write_document', scope, approval_required: false });
    } else {
      expect(fixture.setApprovalPreference).not.toHaveBeenCalled();
    }
    expect(send).toHaveBeenCalledWith(i18n.t('chat.workflowContinue'));
  });
});


describe('external workflow surface boundary', () => {
  it('uses the native running footer and routes stop without saving editors', async () => {
    fixture.session.status = 'active';
    fixture.session.steps![1].status = 'running';
    fixture.session.projection!.current = ['write_document'];
    const control = {
      protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 2,
      continuation: 'awaiting_executor', reviews: [], active_executions: 1, active_execution_ids: ['attempt-1'],
      admission: { can_begin: false }, binding: { bound: true, generation: 1 }, delivery: null,
      available_actions: ['stop'],
    } as WorkflowControlView;
    const execute = vi.fn(async () => {});
    render(<WorkflowPanel conversationId='layout-test' controlAdapter={{ control, execute }} />);
    fireEvent.click(await screen.findByRole('button', { name: '停止' }));
    await waitFor(() => expect(execute).toHaveBeenCalledWith({ kind: 'stop' }));
    expect(fixture.flush).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
  });

  it('renders the same approval bar as native and does not expose rollback while awaiting review', async () => {
    fixture.session.status = 'waiting';
    fixture.session.current_step_id = 'write_document';
    fixture.session.steps!.find(step => step.step_id === 'write_document')!.created_at = '2026-09-16T00:01:00Z';
    fixture.session.projection!.nodes = { write_document: {
      execution: 'succeeded', requires_approval: true, validity: 'effective', reachability: '', readiness: '', branch: '',
    } };
    const review = { id: 'review-1', step_id: 'write_document', execution_id: 'attempt-1',
      status: 'pending' as const, version: 1, manifest_hash: 'hash-1' };
    const control = {
      protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 2,
      continuation: 'awaiting_user', reviews: [review], active_executions: 0, active_execution_ids: [],
      admission: { can_begin: false }, binding: { bound: true, generation: 1 }, delivery: null,
      available_actions: ['confirm', 'confirm_and_continue', 'rewind'],
    } as WorkflowControlView;
    const execute = vi.fn(async () => {});
    render(<WorkflowPanel conversationId='layout-test' controlAdapter={{ control, execute }} />);
    const approval = await screen.findByRole('group', { name: '工作流审批操作' });
    expect(within(approval).getAllByRole('button').map(button => button.textContent)).toEqual([
      '继续执行', '此步骤不需审批', '以后此工作流无需审批',
    ]);
    expect(screen.queryByRole('group', { name: '回退到步骤：' })).not.toBeInTheDocument();
    fireEvent.click(within(approval).getByRole('button', { name: '此步骤不需审批' }));
    await waitFor(() => expect(execute).toHaveBeenCalledWith({
      kind: 'confirm_and_continue', review, preferenceScope: 'step',
    }));
  });

  it('uses the native footer while routing its actions through external control', async () => {
    const execute = vi.fn(async () => {});
    const control = {
      protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 1,
      continuation: 'completed', reviews: [], active_executions: 0, active_execution_ids: [],
      admission: { can_begin: false }, binding: { bound: true, generation: 1 }, delivery: null,
      available_actions: ['rewind'],
    } as WorkflowControlView;
    render(<WorkflowPanel conversationId='layout-test' embedded onRefresh={vi.fn(async () => {})}
      controlAdapter={{ control, execute }} />);
    await screen.findByRole('button', { name: '发布' });
    const shortcuts = screen.getByRole('group', { name: '回退到步骤：' });
    expect(within(shortcuts).getByRole('button', { name: '成稿' })).toBeVisible();
    expect(screen.queryByRole('button', { name: '展开工作流面板' })).not.toBeInTheDocument();
    fireEvent.click(within(shortcuts).getByRole('button', { name: '成稿' }));
    await waitFor(() => expect(execute).toHaveBeenCalledWith({ kind: 'rewind', stepId: 'write_document' }));
    expect(fixture.flush).toHaveBeenCalledOnce();
    expect(fixture.flush.mock.invocationCallOrder[0]).toBeLessThan(execute.mock.invocationCallOrder[0]);
  });

  it('shows the host-backed collapse action for an embedded external panel', async () => {
    const onToggleCollapse = vi.fn();
    const view = render(<WorkflowPanel conversationId='layout-test' embedded onRefresh={vi.fn(async () => {})}
      externalPresentation={{ activities: {}, collapsed: false, onToggleCollapse }} />);
    await screen.findByRole('button', { name: '发布' });
    const button = view.container.querySelector<HTMLButtonElement>('.workflow-panel__collapse-btn')!;
    expect(button).toHaveAccessibleName('收起工作流面板');
    fireEvent.click(button);
    expect(onToggleCollapse).toHaveBeenCalledOnce();
  });

  it('blocks external continuation when the shared editor cannot save', async () => {
    fixture.flush.mockResolvedValue(false);
    const execute = vi.fn(async () => {});
    const control = {
      protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 1,
      continuation: 'completed', reviews: [], active_executions: 0, active_execution_ids: [],
      admission: { can_begin: false }, binding: { bound: true, generation: 1 }, delivery: null,
      available_actions: ['rewind'],
    } as WorkflowControlView;
    render(<WorkflowPanel conversationId='layout-test'
      controlAdapter={{ control, execute }} />);
    await screen.findByRole('button', { name: '发布' });
    fireEvent.click(within(screen.getByRole('group', { name: '回退到步骤：' })).getByRole('button', { name: '成稿' }));
    await waitFor(() => expect(fixture.flush).toHaveBeenCalledOnce());
    expect(execute).not.toHaveBeenCalled();
  });
});


describe('external presentation opt-in', () => {
  it('shows activity and readonly previews only for the external surface, without changing stored artifacts', async () => {
    fixture.session.status = 'active';
    fixture.session.steps![1] = { ...fixture.session.steps![1], status: 'running', task_id: 'task' };
    const original = JSON.stringify(fixture.session);
    const activities = { task: { kind: 'tool' as const, tool: 'search', progress: 45,
      artifacts: [{ slot: 'document', content_type: 'text', seq: 2, value: { text: 'provisional' } }] } };
    const view = render(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities }} />);
    const activity = await screen.findByText('正在调用工具：search');
    expect(activity).toBeVisible();
    expect(activity.closest('.workflow-panel__topbar')).toBeNull();
    expect(activity.closest('.workflow-external-activity')).not.toBeNull();
    expect(await screen.findByText('生成中预览')).toBeVisible();
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '45');
    expect(screen.getByText('正文 document')).toHaveAttribute('data-readonly', 'true');
    expect(screen.getByText('正文 document')).toHaveAttribute('data-value', 'provisional');
    expect(screen.getByText('正文 document')).toHaveAttribute('data-widget', 'text-markdown');
    expect(JSON.stringify(fixture.session)).toBe(original);
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.queryByText('正在调用工具：search')).not.toBeInTheDocument();
    expect(screen.queryByText('生成中预览')).not.toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    expect(screen.getByText('正文 document')).toHaveAttribute('data-readonly', 'false');
    expect(screen.getByText('正文 document')).toHaveAttribute('data-value', '正文');
  });

  it('describes the displayed step rather than another running step, only on external panels', async () => {
    fixture.session.slots = [];
    fixture.session.projection = { nodes: {
      write_document: { execution: 'failed', validity: 'effective', requires_approval: false, reachability: 'reachable', readiness: 'blocked', branch: '' },
      prepare: { execution: 'running', validity: 'effective', requires_approval: false, reachability: 'reachable', readiness: 'blocked', branch: '' },
    } } as WorkflowSession['projection'];
    const view = render(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {} }} />);
    await screen.findByRole('tab', { name: /成稿/ });
    await waitFor(() => expect(screen.getByText('步骤执行失败')).toBeVisible());
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.queryByText('步骤执行失败')).not.toBeInTheDocument();
    expect(view.container.querySelector('.workflow-panel__slot-placeholder')).toHaveTextContent('—');
  });

  it('auto-expands an empty external slot when content arrives and respects an explicit collapse', async () => {
    fixture.session.slots = [];
    fixture.ui.tabs![1].slots[0].widget = { widgetType: 'text-markdown', collapseWhenEmpty: true };
    const view = render(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {} }} />);
    await screen.findByRole('tab', { name: /成稿/ });
    const button = () => view.container.querySelector<HTMLButtonElement>('.workflow-panel__slot-collapse')!;
    expect(button()).toHaveAttribute('aria-expanded', 'false');
    fixture.session.slots = [{ slot_id: 'document', slot: 'document', step_id: 'write_document', selected: true, revision: 1, artifact_value: { text: 'arrived' } }] as WorkflowSession['slots'];
    view.rerender(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {} }} />);
    expect(button()).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('正文 document')).toBeVisible();
    fireEvent.click(button());
    view.rerender(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {} }} />);
    expect(button()).toHaveAttribute('aria-expanded', 'false');
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(button()).toBeNull();
    expect(screen.getByText('正文 document')).toBeVisible();
  });
});


it('keeps external streaming previews from replacing an editor with pending changes', async () => {
  fixture.session.status = 'active';
  fixture.session.steps![1] = { ...fixture.session.steps![1], status: 'running', task_id: 'task' };
  const view = render(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {} }} />);
  await screen.findByRole('tab', { name: /成稿/ });
  fireEvent.doubleClick(screen.getByText('正文 document'));
  view.rerender(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {
    task: { artifacts: [{ slot: 'document', content_type: 'text', seq: 2, value: { text: 'provisional' } }] },
  } }} />);
  expect(screen.queryByText('生成中预览')).not.toBeInTheDocument();
  expect(screen.getByText('正文 document')).toHaveAttribute('data-value', '正文');
  expect(screen.getByText('正文 document')).toHaveAttribute('data-readonly', 'false');
});

it('keeps the native status label on the external surface', async () => {
  render(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {} }} />);
  expect(await screen.findByText('已完成')).toBeVisible();
});

it('keeps saved output visible after execution fails and retries through external control', async () => {
  // API and slot renderer are test doubles. Snapshot normalization, shared panel,
  // previews, controls and command construction below are the real modules.
  const stored = structuredClone(fixture.session);
  stored.status = 'active';
  stored.steps = [{ ...stored.steps![1], status: 'running', task_id: 'task' }];
  const control: WorkflowControlView = {
    protocol: 'workflow.control.v1', session_id: stored.session_id, state_version: 4,
    continuation: 'awaiting_executor', active_executions: 1, active_execution_ids: ['attempt'],
    reviews: [], binding: { bound: true, generation: 1 }, delivery: null,
    admission: { can_begin: false }, available_actions: ['stop'],
  };
  const getControl = vi.fn(async () => ({ data: { data: {
    session: structuredClone(stored), control: structuredClone(control),
    projection: { current: control.active_executions ? ['write_document'] : [] },
  } } }));
  const getSession = vi.fn(async () => { throw new Error('unexpected native fallback'); });
  const getProjection = vi.fn(async () => { throw new Error('unexpected native fallback'); });
  const read = () => loadWorkflowRunSnapshot(stored.session_id, { getControl, getSession, getProjection });
  const write = vi.fn(async () => {});
  const commands = controlActions(async () => (await read()).control!, write, () => 'retry-command');
  let snapshot = await read();
  fixture.session = snapshot.session;
  const activities = { task: { progress: 90, artifacts: [
    { slot: 'document', content_type: 'text', seq: 2, value: { text: 'unfinished preview' } },
  ] } };
  const panel = () => <WorkflowPanel conversationId='layout-test'
    externalPresentation={{ activities }}
    controlAdapter={{ control: snapshot.control!, execute: commands.execute }} />;
  const view = render(panel());
  expect(await screen.findByText('生成中预览')).toBeVisible();
  expect(screen.getByText('正文 document')).toHaveAttribute('data-value', 'unfinished preview');

  // The worker persisted a valid output before a post-step check failed.
  // A leftover streaming preview must neither hide that output nor imply success.
  stored.steps![0].status = 'failed';
  stored.slots![0].artifact_value = { text: 'saved before check failed' };
  control.state_version = 5;
  control.continuation = 'failed';
  control.active_executions = 0;
  control.active_execution_ids = [];
  control.available_actions = ['retry'];
  control.admission = { can_begin: false, reason: 'recovery_required' };
  snapshot = await read();
  fixture.session = snapshot.session;
  view.rerender(panel());

  expect(await screen.findByText(String(i18n.t('chat.workflowStatusFailed')))).toBeVisible();
  expect(screen.queryByText('生成中预览')).not.toBeInTheDocument();
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  expect(screen.getByText('正文 document')).toHaveAttribute('data-value', 'saved before check failed');
  expect(screen.queryByRole('button', { name: String(i18n.t('chat.workflowContinueExecution')) })).not.toBeInTheDocument();
  expect(write).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole('button', { name: String(i18n.t('chat.workflowRetry')) }));
  await waitFor(() => expect(write).toHaveBeenCalledWith({
    command_id: 'retry-command', kind: 'retry', expected_state_version: 5, step_id: 'write_document',
  }));
  expect(write).toHaveBeenCalledOnce();
  expect(fixture.flush.mock.invocationCallOrder[0]).toBeLessThan(write.mock.invocationCallOrder[0]);
  expect(getSession).not.toHaveBeenCalled();
  expect(getProjection).not.toHaveBeenCalled();
});
