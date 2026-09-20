import React, { useContext, useEffect } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { WorkflowSession, WorkflowUI } from '@/modules/chat/store/workflowPanel';
import { SlotEditingContext, WorkflowPanelTabActiveContext } from './slotEditingContext';
import { WorkflowPanel } from './index';

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
  SlotRenderer: ({ slotId }: { slotId: string }) => {
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
    return <article>正文 {slotId}</article>;
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

  it('keeps approval actions inside the panel and preserves approval scope', async () => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.nodes = { write_document: {
      execution: 'succeeded', requires_approval: true, validity: 'effective', reachability: '', readiness: '', branch: '',
    } };
    const send = vi.fn();
    const view = render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    await screen.findByText('正文 document');
    const approval = within(view.container.querySelector('.workflow-panel') as HTMLElement).getByRole('group', { name: '工作流审批操作' });
    expect(within(approval).getByRole('button', { name: '继续执行' })).toBeEnabled();
    const scopeButton = within(approval).getByRole('button', { name: String(i18n.t('chat.workflowSkipThisApproval')) });
    await act(async () => fireEvent.click(scopeButton));
    expect(fixture.setApprovalPreference).toHaveBeenCalledWith('layout-test', { step_id: 'write_document', scope: 'step', approval_required: false });
    expect(send).toHaveBeenCalledWith(i18n.t('chat.workflowContinue'));
  });
});
