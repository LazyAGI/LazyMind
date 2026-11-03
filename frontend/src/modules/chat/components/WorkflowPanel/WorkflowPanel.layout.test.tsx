import React, { useContext, useEffect } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SlotRevision, WorkflowSession, WorkflowUI } from '@/modules/chat/store/workflowPanel';
import { SlotEditingContext, WorkflowPanelTabActiveContext, type SlotEditingContextValue } from './slotEditingContext';
import { WorkflowPanel } from './index';
import { controlActions, externalPanelAction, type WorkflowActionIntent, type WorkflowPanelActionIntent, type WorkflowControlView } from '@/modules/chat/utils/workflowControl';
import { loadWorkflowRunSnapshot } from '@/modules/chat/utils/loadWorkflowRun';
import { ArtifactPreviewContext, useArtifactPreviewController } from '../ArtifactPreview/context';

// Use the same transport boundary as WorkflowRunPage; the panel never receives
// capabilities, reviews or delivery state.
function panelAdapter(control: WorkflowControlView, execute: (intent: WorkflowActionIntent) => Promise<void>) {
  const session = fixture.session;
  return { execute: (intent: WorkflowPanelActionIntent) => execute(externalPanelAction(intent, session, control)) };
}

vi.mock('../ArtifactPreview', () => ({ PreviewContent: ({ target, close }: any) => <section aria-label={target.filename}>
  {target.content}<button onClick={close}>关闭预览</button>
</section> }));

const fixture = vi.hoisted(() => ({
  session: {} as WorkflowSession,
  ui: {} as WorkflowUI,
  fetchWorkflowUI: vi.fn<(...args: [string, string?]) => Promise<WorkflowUI>>(),
  flush: vi.fn(async () => true),
  refresh: vi.fn(),
  pause: vi.fn(),
  setFocusedTab: vi.fn(),
  focusedTab: undefined as string | undefined,
  documentAction: vi.fn(),
  registerFooterAction: undefined as SlotEditingContextValue['registerFooterAction'] | undefined,
}));
vi.mock('@/modules/chat/hooks/useWorkflow', () => ({
  useWorkflowSession: () => ({ session: fixture.session, loading: false, refresh: fixture.refresh }),
}));
vi.mock('@/modules/chat/store/workflowPanel', async (original) => {
  const actual = await original<typeof import('@/modules/chat/store/workflowPanel')>();
  const state = {
    get sessionByConversation() { return { 'layout-test': fixture.session }; },
    bumpDismissedRefresh: vi.fn(), autoRunningByConversation: {}, setAutoRunning: vi.fn(),
    loadActiveSession: vi.fn(async () => {}),
    fetchWorkflowUI: fixture.fetchWorkflowUI, setFocusedTab: fixture.setFocusedTab,
    setFocusedSortOrder: vi.fn(),
    get focusedTabByConversation() { return { 'layout-test': fixture.focusedTab }; }, workflowUIByWorkflow: {},
  };
  return { ...actual, useWorkflowStore: Object.assign((selector?: (value: typeof state) => unknown) => selector ? selector(state) : state, { getState: () => state }) };
});
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => ({ pauseSession: fixture.pause }) }));
vi.mock('@/modules/user/uiPreferencesApi', () => ({
  fetchUserUiPreferences: async () => ({ workflow_trust_notice_dismissed: false }),
  USER_UI_PREFERENCES_CHANGED_EVENT: 'test:preferences-changed',
}));
vi.mock('@/components/StateGraphModal', () => ({ default: () => null }));
vi.mock('./actions/WorkflowTabActions', () => ({ WorkflowTabActions: () => null }));
vi.mock('./ppt/SlideThumb', () => ({ SlideThumb: () => null }));
vi.mock('./SlotComponents', () => ({
  isWriterIrSource: () => false,
  SlotDownloadContext: React.createContext(true),
  SlotMarkdownStream: () => null,
  SlotRenderer: ({ slotId, slot, readOnly, widget }: { widget?: { widgetType?: string }; slotId: string; slot: SlotRevision; readOnly?: boolean }) => {
    const context = useContext(SlotEditingContext);
    fixture.registerFooterAction = context.registerFooterAction;
    const active = useContext(WorkflowPanelTabActiveContext);
    useEffect(() => {
      if (!active) return;
      if (slot.document) {
        const key = `${slotId}:${slot.list_index ?? -1}`;
        const kinds = slot.document.editable ? ['copy', 'download', 'publish'] : ['copy', 'download'];
        const unregister = kinds.map(kind => context.registerFooterAction(`${key}:${kind}`, {
          label: { copy: '复制内容', download: '下载', publish: '发布' }[kind]!,
          onClick: () => fixture.documentAction(key, kind, 'markdown'),
          ...(kind === 'download' ? { menuLabel: '选择下载格式', menu: [{ key: 'text', label: '纯文本',
            onClick: () => fixture.documentAction(key, kind, 'text') }] } : {}),
        }));
        return () => unregister.forEach(remove => remove());
      }
      const unregisterFlush = context.registerFlush(slotId, fixture.flush);
      const unregisterAction = context.registerFooterAction(slotId, {
        label: '发布', onClick: vi.fn(), statusText: '已写回云文档', statusTone: 'success',
        statusLink: { href: 'https://example.com/document', label: '打开云文档' },
      });
      return () => { unregisterFlush(); unregisterAction(); };
    }, [active, context.registerFlush, context.registerFooterAction, slotId, slot.document, slot.list_index]);
    return <article onDoubleClick={() => context.setEditing(slotId, true)} data-widget={widget?.widgetType} data-readonly={Boolean(readOnly)} data-value={(slot.artifact_value as { text?: string })?.text}>正文 {slotId}<button onClick={() => context.onBrowse?.()}>查看历史</button></article>;
  },
}));

beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  vi.clearAllMocks();
  fixture.fetchWorkflowUI.mockReset().mockImplementation(async () => fixture.ui);
  fixture.flush.mockReset().mockResolvedValue(true);
  fixture.pause.mockReset().mockResolvedValue(undefined);
  fixture.focusedTab = undefined;
  fixture.setFocusedTab.mockImplementation((_id: string, tab: string) => { fixture.focusedTab = tab; });
  localStorage.clear();
  fixture.ui = { name: '示例工作流', tabs: [
    { id: 'prepare', step_id: 'prepare', label: '写作准备', layout: 'list', slots: [] },
    { id: 'draft', step_id: 'write_document', label: '成稿', layout: 'list', slots: [
      { id: 'document', label: '成稿', type: 'text', widget: { widgetType: 'writer-document' } },
    ] },
  ] };
  fixture.session = {
    session_id: 'layout-test', conversation_id: 'layout-test', workflow_id: 'fixture', workflow_mode: 'auto',
    controller_host: 'lazymind',
    status: 'completed', current_step_id: 'write_document', created_at: '', updated_at: '',
    projection: { display: { status: 'completed', reason: 'completed', approval_step_id: '' }, past: ['prepare', 'write_document'] },
    steps: ['prepare', 'write_document', 'stale_step'].map(step_id => ({
      id: step_id, session_id: 'layout-test', step_id, attempt: 1, task_id: '', status: 'succeeded',
      validity: step_id === 'stale_step' ? 'stale' : 'effective', created_at: '2026-09-16T00:00:00Z', updated_at: '',
    })),
    slots: [{ slot_id: 'document', slot: 'document', step_id: 'write_document', selected: true, revision: 1, artifact_value: { text: '正文' } }] as WorkflowSession['slots'],
  };
});
afterEach(cleanup);

async function waitForCompletedContinueButton() {
  await screen.findByRole('tab', { name: '成稿', selected: true });
  return waitFor(() => {
    const button = screen.getByRole('button', { name: '继续' });
    expect(button).toBeEnabled();
    return button;
  });
}

describe('shared workflow compact layout', () => {
  it('replaces the UI for a new pinned version of the same workflow without displaying the old tabs', async () => {
    fixture.session.pinned_revision_id = 'v1';
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿' });
    expect(fixture.fetchWorkflowUI).toHaveBeenLastCalledWith('fixture', 'v1');
    let resolve!: (ui: WorkflowUI) => void;
    fixture.fetchWorkflowUI.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    fixture.session = { ...fixture.session, pinned_revision_id: 'v2' };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.queryByRole('tab', { name: '成稿' })).not.toBeInTheDocument();
    expect(fixture.fetchWorkflowUI).toHaveBeenLastCalledWith('fixture', 'v2');
    await act(async () => resolve({ tabs: [
      { id: 'prepare', label: '新版准备', slots: [] },
      { id: 'review', label: '新增审核', slots: [] },
      { id: 'draft', label: '新版结果', slots: [] },
    ] }));
    expect(screen.getAllByRole('tab').map(tab => tab.textContent)).toEqual(['1新版准备', '2新增审核', '3新版结果']);
  });

  it('ignores a late UI response after switching pinned versions', async () => {
    let resolveOld!: (ui: WorkflowUI) => void;
    fixture.fetchWorkflowUI.mockImplementationOnce(() => new Promise(done => { resolveOld = done; }));
    fixture.session.pinned_revision_id = 'v1';
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    fixture.session = { ...fixture.session, session_id: 'new-session', pinned_revision_id: 'v2' };
    fixture.ui = { tabs: [{ id: 'draft', label: '新版结果', slots: [] }] };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '新版结果' });
    await act(async () => resolveOld({ tabs: [{ id: 'draft', label: '过期结果', slots: [] }] }));
    expect(screen.queryByRole('tab', { name: '过期结果' })).not.toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '新版结果' })).toBeVisible();
  });

  it('does not keep the previous locale visible while loading the same revision in another language', async () => {
    fixture.session.pinned_revision_id = 'v1';
    render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿' });
    let resolve!: (ui: WorkflowUI) => void;
    fixture.fetchWorkflowUI.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    await act(async () => { await i18n.changeLanguage('en-US'); });
    expect(screen.queryByRole('tab', { name: '成稿' })).not.toBeInTheDocument();
    await act(async () => resolve({ tabs: [{ id: 'draft', label: 'Result', slots: [] }] }));
    expect(screen.getByRole('tab', { name: 'Result' })).toBeVisible();
  });

  it.each(['native', 'codex'] as const)('opens the actual completed result instead of the last tab on %s', async surface => {
    fixture.ui.tabs!.reverse();
    fixture.session.current_step_id = '';
    fixture.session.steps = fixture.session.steps!.map((step, i) => ({
      ...step, updated_at: `2026-09-16T00:0${i}:00Z`,
    }));
    const props = surface === 'codex' ? { embedded: true, externalPresentation: { activities: {}, compactEmptyStates: false } } : {};
    const view = render(<WorkflowPanel conversationId='layout-test' {...props} />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    expect(screen.getByRole('tab', { name: '写作准备', selected: false })).toBeVisible();
    expect(fixture.setFocusedTab).not.toHaveBeenCalled();
    view.unmount();
    render(<WorkflowPanel conversationId='layout-test' {...props} />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
  });

  it('preserves a user-selected page when a workflow completes', async () => {
    fixture.focusedTab = 'prepare';
    fixture.session.current_step_id = '';
    render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '写作准备', selected: true });
  });

  it('keeps automatic grids from reintroducing hidden output when every tab is hidden', async () => {
    fixture.ui.slotDefinitions = [{ id: 'document', label: '内部数据', type: 'text', user_visible: false }];
    fixture.ui.tabs = [{ id: 'internal', label: '内部数据', slots: fixture.ui.slotDefinitions }];
    const { setDeveloperModeActive } = await import('@/utils/developerMode');
    render(<WorkflowPanel conversationId='layout-test' />);
    await waitFor(() => expect(screen.getByText('示例工作流')).toBeInTheDocument());
    expect(screen.queryByText('正文 document')).not.toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: '内部数据' })).not.toBeInTheDocument();
    act(() => setDeveloperModeActive(true));
    expect(await screen.findByText('正文 document')).toBeInTheDocument();
  });

  it('switches declared output visibility with developer mode without changing the workflow session', async () => {
    fixture.ui.tabs![0].slots = [{ id: 'internal_prompt', label: '生成提示词', type: 'text', user_visible: false }];
    fixture.session.slots!.push({ ...fixture.session.slots![0], slot_id: 'internal_prompt', slot: 'internal_prompt', step_id: 'prepare' });
    const { setDeveloperModeActive } = await import('@/utils/developerMode');
    render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿' });
    expect(screen.queryByRole('tab', { name: '写作准备' })).not.toBeInTheDocument();
    expect(screen.queryByText('正文 internal_prompt')).not.toBeInTheDocument();
    act(() => setDeveloperModeActive(true));
    fireEvent.click(await screen.findByRole('tab', { name: '写作准备' }));
    expect(screen.getByText('正文 internal_prompt')).toBeInTheDocument();
    act(() => setDeveloperModeActive(false));
    expect(screen.queryByRole('tab', { name: '写作准备' })).not.toBeInTheDocument();
    expect(fixture.session.slots!.some(slot => slot.slot_id === 'internal_prompt')).toBe(true);
  });

  it('leaves expanded workflow mode when previewing an ordinary reply in the same conversation', async () => {
    const controller = { target: null, open: vi.fn(), close: vi.fn(), onVersions: undefined };
    const view = render(<ArtifactPreviewContext.Provider value={controller}><WorkflowPanel conversationId="layout-test" /></ArtifactPreviewContext.Provider>);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    fireEvent.click(screen.getByRole('button', { name: '展开工作流面板' }));
    expect(document.querySelector('.workflow-panel--expanded')).not.toBeNull();
    view.rerender(<ArtifactPreviewContext.Provider value={{ ...controller, target: { key: 'ordinary', filename: 'report.md', content: 'report' } }}>
      <WorkflowPanel conversationId="layout-test" />
    </ArtifactPreviewContext.Provider>);
    await waitFor(() => expect(document.querySelector('.workflow-panel--expanded')).toBeNull());
    expect(screen.getByText('正文 document')).toBeInTheDocument();
    expect(fixture.refresh).not.toHaveBeenCalled();
  });

  it('opens an auxiliary output in a shared result tab through status_step_ids', async () => {
    fixture.ui.tabs![1].status_step_ids = ['write_document', 'write_flat_document'];
    const controller = { target: { key: 'visual:1', filename: 'visual.json', content: 'visual plan',
      workflow: { sessionId: 'layout-test', stepId: 'write_flat_document' } }, open: vi.fn(), close: vi.fn(), onVersions: undefined };
    render(<ArtifactPreviewContext.Provider value={controller}><WorkflowPanel conversationId="layout-test" /></ArtifactPreviewContext.Provider>);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    expect(screen.getByRole('region', { name: 'visual.json' })).toHaveTextContent('visual plan');
    expect(controller.close).not.toHaveBeenCalled();
    fireEvent.keyDown(screen.getByRole('region', { name: 'visual.json' }).parentElement!, { key: 'Escape' });
    expect(controller.close).toHaveBeenCalledOnce();
  });

  it('locates a stage output inside its workflow step without unmounting the editor', async () => {
    function Host() {
      const controller = useArtifactPreviewController('layout-test');
      return <ArtifactPreviewContext.Provider value={controller}>
        <button onClick={() => controller.open({ key: 'plan:2', filename: 'plan.json', content: 'exact plan revision 2',
          workflow: { sessionId: 'layout-test', stepId: 'prepare' } })}>预览计划</button>
        <WorkflowPanel conversationId='layout-test' />
      </ArtifactPreviewContext.Provider>;
    }
    render(<Host />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    const editor = screen.getByText('正文 document');
    fireEvent.click(screen.getByRole('button', { name: '预览计划' }));
    await screen.findByRole('tab', { name: '写作准备', selected: true });
    expect(screen.getByRole('region', { name: 'plan.json' }).closest('.workflow-panel__body')).not.toBeNull();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(editor).toBeInTheDocument();
    expect(editor).not.toBeVisible();
    expect(fixture.refresh).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('tab', { name: '成稿' }));
    expect(screen.queryByRole('region', { name: 'plan.json' })).not.toBeInTheDocument();
    expect(editor).toBeVisible();
  });

  it('keeps a replacement document action when the previous registration cleans up', async () => {
    render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    let unregisterOld!: () => void;
    let unregisterNew!: () => void;
    const onClick = vi.fn();
    act(() => { unregisterOld = fixture.registerFooterAction!('copy', { label: '旧复制', onClick: vi.fn() }); });
    act(() => { unregisterNew = fixture.registerFooterAction!('copy', { label: '新复制', onClick }); });
    act(() => unregisterOld());

    fireEvent.click(screen.getByRole('button', { name: '新复制' }));
    expect(onClick).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: '旧复制' })).not.toBeInTheDocument();
    act(() => unregisterNew());
    expect(screen.queryByRole('button', { name: '新复制' })).not.toBeInTheDocument();
  });

  it('shows chapter progress until the full document exists, then folds process materials without losing their actions', async () => {
    fixture.ui.tabs![1].generation_process = { result_slot: 'document', slots: ['chapters', 'report'] };
    fixture.ui.tabs![1].slots = [
      { id: 'document', label: '全文', type: 'file' },
      { id: 'chapters', label: '分章稿', type: 'file', cardinality: 'list' },
      { id: 'report', label: '检查报告', type: 'text' },
    ];
    const doc = (slot: string, editable = false): SlotRevision => ({
      slot_id: slot, slot, list_index: -1, step_id: 'write_document', selected: true, revision: 1,
      artifact_id: slot, artifact_value: { text: slot },
      document: { representation: 'markdown', editable, capabilities: ['convert_document'] },
    } as SlotRevision);
    fixture.session.slots = [doc('chapters'), doc('report')];
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    expect(screen.getByText('正文 chapters')).toBeVisible();
    expect(screen.queryByRole('button', { name: '生成过程' })).not.toBeInTheDocument();

    fixture.session = { ...fixture.session, slots: [doc('document', true), ...fixture.session.slots!] };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.getByText('正文 document')).toBeVisible();
    const toggle = screen.getByRole('button', { name: '生成过程' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('正文 chapters')).not.toBeInTheDocument();
    expect(screen.queryByText('正文 report')).not.toBeInTheDocument();
    const footer = within(screen.getByRole('group', { name: '工作流会话操作' }));
    expect(footer.getAllByRole('button', { name: '复制内容' })).toHaveLength(1);
    fireEvent.click(toggle);
    await waitFor(() => expect(screen.getByText('正文 chapters')).toBeVisible());
    expect(screen.getByText('正文 report')).toBeVisible();
    fireEvent.click(within(screen.getByRole('group', { name: '检查报告' }))
      .getByRole('button', { name: '下载' }));
    expect(fixture.documentAction).toHaveBeenLastCalledWith('report:-1', 'download', 'markdown');
    expect(footer.getAllByRole('button', { name: '复制内容' })).toHaveLength(1);

    // A normal refresh preserves the user's expanded view.
    fixture.session = { ...fixture.session, slots: [...fixture.session.slots!] };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    // If the full result is invalidated, progress is visible again.
    fixture.session = { ...fixture.session, slots: [doc('chapters'), doc('report')] };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.queryByRole('button', { name: '生成过程' })).not.toBeInTheDocument();
    expect(screen.getByText('正文 chapters')).toBeVisible();
  });

  it.each(['running', 'pending'])('keeps process material visible while the producer is %s even with an older result', async status => {
    fixture.ui.tabs![1].generation_process = { result_slot: 'document', slots: ['report'] };
    fixture.ui.tabs![1].slots.push({ id: 'report', label: '检查报告', type: 'text' });
    fixture.session.slots!.push({ ...fixture.session.slots![0], slot_id: 'report', slot: 'report' });
    fixture.session.steps = fixture.session.steps!.map(step => step.step_id === 'write_document' ? { ...step, status } : step);
    render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: /^成稿/, selected: true });
    expect(screen.getByText('正文 report')).toBeVisible();
    expect(screen.queryByRole('button', { name: '生成过程' })).not.toBeInTheDocument();
  });

  it('keeps main document actions in the footer and scopes chapter/report actions to their own documents', async () => {
    fixture.ui.tabs![1].slots = [
      { id: 'document', label: '论文初稿', type: 'file' },
      { id: 'chapters', label: '分章稿', type: 'file', cardinality: 'list' },
      { id: 'report', label: '检查报告', type: 'text' },
    ];
    const doc = (slot: string, list_index: number, editable = false): SlotRevision => ({
      slot_id: slot, slot, list_index, step_id: 'write_document', selected: true, revision: 1,
      artifact_id: `${slot}-${list_index}`, artifact_value: { text: slot },
      document: { representation: 'markdown', editable, capabilities: ['convert_document'] },
    } as SlotRevision);
    fixture.session.slots = [doc('document', -1, true), ...Array.from({ length: 7 }, (_, i) => doc('chapters', i)), doc('report', -1)];
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    const footer = within(screen.getByRole('group', { name: '工作流会话操作' }));
    await waitFor(() => expect(footer.getAllByRole('button', { name: '复制内容' })).toHaveLength(1));
    expect(footer.getAllByRole('button', { name: '下载' })).toHaveLength(1);
    expect(footer.getAllByRole('button', { name: '发布' })).toHaveLength(1);
    const chapter = within(screen.getByRole('group', { name: '分章稿 · 4' }));
    fireEvent.click(chapter.getByRole('button', { name: '复制内容' }));
    expect(fixture.documentAction).toHaveBeenLastCalledWith('chapters:3', 'copy', 'markdown');
    fireEvent.click(chapter.getByRole('button', { name: '选择下载格式' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: '纯文本' }));
    expect(fixture.documentAction).toHaveBeenLastCalledWith('chapters:3', 'download', 'text');
    fireEvent.click(within(screen.getByRole('group', { name: '检查报告' })).getByRole('button', { name: '下载' }));
    expect(fixture.documentAction).toHaveBeenLastCalledWith('report:-1', 'download', 'markdown');
    fireEvent.click(footer.getByRole('button', { name: '复制内容' }));
    expect(fixture.documentAction).toHaveBeenLastCalledWith('document:-1', 'copy', 'markdown');
    fireEvent.click(screen.getByRole('tab', { name: '写作准备' }));
    expect(screen.queryByRole('button', { name: '复制内容' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '成稿' }));
    await waitFor(() => expect(within(screen.getByRole('group', { name: '工作流会话操作' })).getAllByRole('button', { name: '下载' })).toHaveLength(1));
    // Revisions of the same document update without accumulating old actions.
    fixture.session = { ...fixture.session, slots: fixture.session.slots!.map(slot => ({ ...slot, revision: 2 })) };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.getAllByRole('button', { name: '复制内容' })).toHaveLength(9);
  });

  it.each(['native', 'codex'] as const)('follows steps until the user selects a tab on %s', async surface => {
    const props = surface === 'codex' ? { embedded: true, externalPresentation: { activities: {}, compactEmptyStates: false } } : {};
    fixture.session = { ...fixture.session, status: 'active', current_step_id: 'prepare' };
    const view = render(<WorkflowPanel conversationId='layout-test' {...props} />);
    await waitFor(() => expect(screen.getByRole('tab', { name: '写作准备' })).toHaveAttribute('aria-selected', 'true'));
    fixture.session = { ...fixture.session, current_step_id: 'write_document' };
    view.rerender(<WorkflowPanel conversationId='layout-test' {...props} />);
    await waitFor(() => expect(screen.getByRole('tab', { name: '成稿' })).toHaveAttribute('aria-selected', 'true'));
    fireEvent.click(screen.getByRole('tab', { name: '写作准备' }));
    fixture.session = { ...fixture.session, status: 'waiting', slots: [...fixture.session.slots!] };
    view.rerender(<WorkflowPanel conversationId='layout-test' {...props} />);
    expect(screen.getByRole('tab', { name: '写作准备' })).toHaveAttribute('aria-selected', 'true');
  });

  it('keeps Codex empty slots and default folding identical to native content', async () => {
    fixture.session.slots = [];
    fixture.ui.tabs![1].slots[0].widget = { widgetType: 'text-markdown' };
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    const nativeContent = view.container.querySelector('.workflow-panel__body')!.innerHTML;
    view.rerender(<WorkflowPanel conversationId='layout-test' embedded
      externalPresentation={{ activities: {}, compactEmptyStates: false }} />);
    expect(view.container.querySelector('.workflow-panel__body')!.innerHTML).toBe(nativeContent);
    expect(view.container.querySelector('.workflow-panel__slot-placeholder')).toBeNull();
    expect(view.container.querySelector('.workflow-panel__slot-collapse')).toBeNull();
  });

  it.each(['list', 'composite'] as const)('hides missing components in %s tabs and reveals arriving output', async layout => {
    fixture.ui.tabs![1].layout = layout;
    fixture.session.slots![0].sort_order = 1;
    fixture.ui.tabs![1].slots.push({ id: 'template_content_brief', label: '模板内容要求', type: 'text' });
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    expect(screen.getByText('正文 document')).toBeVisible();
    expect(screen.queryByText('模板内容要求')).not.toBeInTheDocument();
    expect(view.container.querySelector('.workflow-panel__slot-placeholder')).toBeNull();
    fixture.session = { ...fixture.session, slots: [...fixture.session.slots!, {
      slot_id: 'template-brief', slot: 'template_content_brief', step_id: 'write_document',
      selected: true, revision: 1, sort_order: 1, artifact_value: { text: '模板内容' },
    }] } as WorkflowSession;
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.getByText('模板内容要求')).toBeVisible();
    expect(screen.getByText('正文 template_content_brief')).toBeVisible();
  });

  it.each(['active', 'waiting', 'failed', 'completed'] as const)(
    'keeps the full trust notice hidden for %s sessions and after remount', async (status) => {
      fixture.session.status = status;
      fixture.session.steps = [];
      const notice = '此工作流以完全信任模式运行，可在服务进程的系统权限范围内读取、修改文件和执行代码，不受 Workspace 权限限制，无需逐次审批。';
      const view = render(<WorkflowPanel conversationId='layout-test' />);
      await screen.findByRole('tab', { name: '成稿' });
      expect(screen.queryByText(notice)).not.toBeInTheDocument();
      view.unmount();
      render(<WorkflowPanel conversationId='layout-test' />);
      await screen.findByRole('tab', { name: '成稿' });
      expect(screen.queryByText(notice)).not.toBeInTheDocument();
    },
  );

  it.each(['completed', 'failed'] as const)('binds the %s rerun to Chat and still saves before rerunning', async (status) => {
    fixture.session.status = status;
    fixture.session.projection!.display!.status = status;
    const send = vi.fn();
    // Let the asynchronous UI load and editor-action registration settle before
    // asserting the save-before-rerun contract.
    await act(async () => { render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />); });
    await screen.findByRole('button', { name: '发布' });
    const shortcuts = screen.getByRole('group', { name: '回退到步骤：' });
    expect(shortcuts).toBeVisible();
    expect(within(shortcuts).getByRole('button', { name: '写作准备' })).toBeVisible();
    fireEvent.click(within(shortcuts).getByRole('button', { name: '成稿' }));
    await waitFor(() => expect(send).toHaveBeenCalledWith('请重新执行步骤 write_document', {
      session_id: 'layout-test', kind: 'rewind', step_id: 'write_document', expected_state_version: 0,
    }));
    expect(fixture.flush).toHaveBeenCalledOnce();
    expect(within(shortcuts).queryByRole('button', { name: 'stale_step' })).not.toBeInTheDocument();
  });

  it('does not rerun when saving the current edits fails', async () => {
    fixture.flush.mockResolvedValue(false);
    const send = vi.fn();
    await act(async () => { render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />); });
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

  it('labels grouped artifact tabs without suggesting they count execution tasks', async () => {
    fixture.ui.task_presentation = { grouped: true, hidden_steps: ['route'] };
    fixture.session.steps!.unshift({ ...fixture.session.steps![0], id: 'route', step_id: 'route' });
    render(<WorkflowPanel conversationId='layout-test' />);
    const tabs = await screen.findByRole('tablist', { name: '产物' });
    expect(within(tabs).getAllByRole('tab')).toHaveLength(2);
    expect(tabs.querySelectorAll('.workflow-panel__tab-badge')).toHaveLength(0);
    fireEvent.click(within(tabs).getByRole('tab', { name: '写作准备' }));
    expect(fixture.setFocusedTab).toHaveBeenCalledWith('layout-test', 'prepare');
  });

  it('keeps long step navigation usable with the keyboard and preserves multiple slot headings', async () => {
    fixture.ui.tabs = Array.from({ length: 7 }, (_, i) => ({ id: `step${i}`, label: `阶段 ${i + 1}`, layout: 'list', slots: [] }));
    fixture.ui.tabs[6].slots = [
      { id: 'main', label: '主文档', type: 'text', widget: { widgetType: 'writer-document' } },
      { id: 'evidence', label: '证据清单', type: 'text' },
    ];
    fixture.session.slots = ['main', 'evidence'].map(slot => ({
      ...fixture.session.slots![0], slot_id: slot, slot, step_id: 'step6',
    }));
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
    fixture.session.status = 'active';
    fixture.session.projection!.display = { status: 'running', reason: 'executing', approval_step_id: '' };
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: /成稿/ });
    expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '继续' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '停止' }));
    await waitFor(() => expect(fixture.pause).toHaveBeenCalledWith('layout-test'));
    expect(fixture.pause).toHaveBeenCalledOnce();
    expect(fixture.refresh).toHaveBeenCalledOnce();
    expect(fixture.flush).not.toHaveBeenCalled();
    fixture.session = { ...fixture.session, status: 'pausing' };
    fixture.session.projection!.display = { status: 'running', reason: 'pausing', approval_step_id: '' };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.getByRole('button', { name: '停止中…' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '停止中…' }));
    expect(fixture.pause).toHaveBeenCalledOnce();
    fixture.session = { ...fixture.session, status: 'failed' };
    fixture.session.projection!.display = { status: 'failed', reason: 'execution_failed', approval_step_id: '' };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled();
  });

  it.each([false, true])('pauses only the workflow while Chat busy=%s', async chatBusy => {
    fixture.session.status = 'active';
    fixture.session.projection!.display = {
      status: 'running', reason: 'executing', approval_step_id: '',
    };
    const send = vi.fn();
    render(<WorkflowPanel conversationId='layout-test' chatBusy={chatBusy} onSendMessage={send} />);
    await act(async () => fireEvent.click(await screen.findByRole('button', { name: '停止' })));
    expect(fixture.pause).toHaveBeenCalledWith('layout-test');
    expect(fixture.pause).toHaveBeenCalledOnce();
    expect(send).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '停止' })).toHaveAttribute('title', i18n.t('chat.workflowPauseScopeHint'));
  });

  it.each(['zh-CN', 'en-US'])('keeps stop labels distinct from confirmed pause through the lifecycle (%s)', async language => {
    await i18n.changeLanguage(language);
    const labels = language === 'zh-CN'
      ? { stop: '停止', stopping: '停止中…', running: '执行中', paused: '已暂停', resume: '继续' }
      : { stop: 'Stop', stopping: 'Stopping…', running: 'Running', paused: 'Paused', resume: 'Continue' };
    fixture.session.status = 'active';
    fixture.session.projection!.display = { status: 'running', reason: 'executing', approval_step_id: '' };
    let finishStop!: () => void;
    fixture.pause.mockImplementationOnce(() => new Promise<void>(resolve => { finishStop = resolve; }));
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    fireEvent.click(await screen.findByRole('button', { name: labels.stop }));
    const stopping = screen.getByRole('button', { name: labels.stopping });
    expect(stopping).toBeDisabled();
    fireEvent.click(stopping);
    expect(fixture.pause).toHaveBeenCalledOnce();
    expect(screen.getByRole('button', { name: String(i18n.t('chat.workflowStatusAria', { status: labels.running })) })).toBeVisible();
    expect(screen.getByText(new RegExp(String(i18n.t('chat.workflowStoppingHint'))))).toBeVisible();
    expect(screen.queryByRole('button', { name: labels.resume })).not.toBeInTheDocument();

    fixture.session = { ...fixture.session, status: 'pausing', projection: {
      ...fixture.session.projection, display: { status: 'running', reason: 'pausing', approval_step_id: '' },
    } };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    await act(async () => finishStop());
    expect(screen.getByRole('button', { name: labels.stopping })).toBeDisabled();
    // The confirmed state, not the HTTP response alone, enables Continue.
    fixture.session = { ...fixture.session, status: 'waiting', projection: {
      ...fixture.session.projection, current: [], ready: ['write_document'],
      display: { status: 'paused', reason: 'user_stopped', approval_step_id: '' },
    } };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.getByRole('button', { name: labels.resume })).toBeEnabled();
    expect(screen.getByRole('button', { name: String(i18n.t('chat.workflowStatusAria', { status: labels.paused })) })).toBeVisible();
    expect(screen.queryByRole('button', { name: labels.stopping })).not.toBeInTheDocument();

    fixture.session = { ...fixture.session, status: 'active', projection: {
      ...fixture.session.projection, display: { status: 'running', reason: 'executing', approval_step_id: '' },
    } };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.getByRole('button', { name: labels.stop })).toBeEnabled();
    expect(screen.queryByRole('button', { name: labels.resume })).not.toBeInTheDocument();
  });

  it('releases a failed pause request for retry without inventing paused state', async () => {
    fixture.session.status = 'active';
    fixture.session.projection!.display = { status: 'running', reason: 'executing', approval_step_id: '' };
    fixture.pause.mockRejectedValueOnce(new Error('cancel notification failed'));
    render(<WorkflowPanel conversationId='layout-test' />);
    await act(async () => fireEvent.click(await screen.findByRole('button', { name: '停止' })));
    expect(screen.getByRole('button', { name: '停止' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: '继续' })).not.toBeInTheDocument();
    expect(fixture.refresh).not.toHaveBeenCalled();
  });

  it.each(['paused-before-dispatch', 'failed-with-ready-step', 'completed'] as const)(
    'offers Continue only for confirmed paused ready steps: %s', async (scenario) => {
      const status = scenario === 'paused-before-dispatch' ? 'paused' : scenario === 'completed' ? 'completed' : 'failed';
      fixture.session.status = status === 'paused' ? 'waiting' : status;
      fixture.session.projection!.display = { status, reason: '', approval_step_id: '' };
      fixture.session.projection!.ready = ['write_document'];
      fixture.session.projection!.current = [];
      const send = vi.fn();
      render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
      await screen.findByRole('tab', { name: '成稿' });
      if (status === 'paused') {
        await act(async () => fireEvent.click(screen.getByRole('button', { name: '继续' })));
        expect(send).toHaveBeenCalledWith('继续', { session_id: 'layout-test', kind: 'resume', expected_state_version: 0 });
        expect(send).toHaveBeenCalledOnce();
      } else {
        expect(screen.queryByRole('button', { name: '继续' })).not.toBeInTheDocument();
        expect(send).not.toHaveBeenCalled();
      }
      if (status === 'failed') expect(screen.getByRole('button', { name: '重试' })).toBeEnabled();
    },
  );

  it('preserves the workflow-declared completed Continue action without treating it as resume', async () => {
    fixture.ui.tabs![1].completed_continue_step = 'write_document';
    const send = vi.fn();
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    const button = await waitForCompletedContinueButton();
    await act(async () => fireEvent.click(button));
    await waitFor(() => expect(send).toHaveBeenCalledOnce());
    expect(send).toHaveBeenCalledWith(`${i18n.t('chat.workflowRollbackPrefix')}write_document`, {
      session_id: 'layout-test', kind: 'rewind', step_id: 'write_document', expected_state_version: 0,
    });
  });

  it('does not change completed state when Chat refuses the continuation', async () => {
    fixture.ui.tabs![1].completed_continue_step = 'write_document';
    const send = vi.fn(async () => false);
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    const button = await waitForCompletedContinueButton();
    fireEvent.click(button);
    await waitFor(() => expect(send).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.getByRole('button', { name: '继续' })).toBeEnabled());
    expect(fixture.session.projection!.display!.status).toBe('completed');
    expect(fixture.pause).not.toHaveBeenCalled();
  });

  it('keeps interrupted-step resume protected across Chat acceptance and panel remount', async () => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.display = { status: 'paused', reason: 'user_stopped', approval_step_id: '' };
    fixture.session.projection!.current = ['write_document'];
    fixture.session.projection!.nodes = { write_document: {
      execution: 'interrupted', requires_approval: false, validity: 'effective', reachability: '', readiness: '', branch: '',
    } };
    let accept!: (accepted: boolean) => void;
    const send = vi.fn(() => new Promise<boolean>(resolve => { accept = resolve; }));
    const view = render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    await screen.findByRole('tab', { name: '成稿' });
    const resume = screen.getByRole('button', { name: '继续' });
    fireEvent.click(resume);
    expect(resume).toBeDisabled();
    fireEvent.click(resume);
    await waitFor(() => expect(send).toHaveBeenCalledOnce());
    expect(send).toHaveBeenCalledWith('继续', { session_id: 'layout-test', kind: 'resume', expected_state_version: 0 });
    // Sending inserts a new assistant message: its panel has no local actionPending.
    view.rerender(<WorkflowPanel key='new-turn' conversationId='layout-test' onSendMessage={send} chatBusy />);
    await act(async () => accept(true));
    fireEvent.click(screen.getByRole('button', { name: '继续' }));
    expect(screen.getByRole('button', { name: '继续' })).toBeDisabled();
    expect(send).toHaveBeenCalledOnce();
    expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument();

    fixture.session = { ...fixture.session, status: 'active' };
    fixture.session.projection!.display = { status: 'running', reason: 'executing', approval_step_id: '' };
    view.rerender(<WorkflowPanel key='new-turn' conversationId='layout-test' onSendMessage={send} chatBusy />);
    expect(screen.queryByRole('button', { name: '继续' })).not.toBeInTheDocument();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '停止' })));
    expect(fixture.pause).toHaveBeenCalledWith('layout-test');
    expect(send).toHaveBeenCalledOnce();
    fixture.session = { ...fixture.session, status: 'waiting' };
    fixture.session.projection!.display = { status: 'paused', reason: 'user_stopped', approval_step_id: '' };
    view.rerender(<WorkflowPanel key='new-turn' conversationId='layout-test' onSendMessage={send} />);
    expect(screen.getByRole('button', { name: '继续' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument();
  });

  it('releases resume when Chat rejects the request', async () => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.display = { status: 'paused', reason: 'user_stopped', approval_step_id: '' };
    fixture.session.projection!.current = ['write_document'];
    fixture.session.projection!.nodes = { write_document: {
      execution: 'interrupted', requires_approval: false, validity: 'effective', reachability: '', readiness: '', branch: '',
    } };
    const send = vi.fn(async () => false);
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    await screen.findByRole('tab', { name: '成稿' });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '继续' })));
    expect(send).toHaveBeenCalledOnce();
    expect(screen.getByRole('button', { name: '继续' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument();
  });

  it('blocks approval while an ordinary Chat turn is active without treating it as approval', async () => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.display = { status: 'waiting', reason: 'approval_required', approval_step_id: 'write_document' };
    const send = vi.fn();
    const view = render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} chatBusy />);
    const approve = await screen.findByRole('button', { name: '继续执行' });
    expect(approve).toBeDisabled();
    fireEvent.click(approve);
    expect(send).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument();
    view.rerender(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    expect(screen.getByRole('button', { name: '继续执行' })).toBeEnabled();
  });

  it('binds a failed workflow retry to the admitted Chat turn', async () => {
    fixture.session.status = 'failed';
    fixture.session.state_version = 7;
    fixture.session.projection!.display = { status: 'failed', reason: 'execution_failed', approval_step_id: '' };
    const send = vi.fn(async () => false);
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    const retry = await screen.findByRole('button', { name: '重试' });
    await act(async () => fireEvent.click(retry));
    expect(send).toHaveBeenCalledWith('重试', { session_id: 'layout-test', kind: 'retry', expected_state_version: 7 });
    // A refused Chat request must not optimistically start the workflow.
    expect(retry).toBeEnabled();
    expect(screen.queryByRole('button', { name: '停止' })).not.toBeInTheDocument();
  });

  it('offers retry after pausing a parallel run with an already failed branch', async () => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.display = { status: 'paused', reason: 'user_stopped', approval_step_id: '' };
    fixture.session.projection!.nodes = {
      failed: { execution: 'failed', requires_approval: false, validity: 'effective', reachability: '', readiness: '', branch: '' },
      interrupted: { execution: 'interrupted', requires_approval: false, validity: 'effective', reachability: '', readiness: '', branch: '' },
    };
    fixture.session.projection!.current = ['failed', 'interrupted'];
    const send = vi.fn();
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    expect(await screen.findByRole('button', { name: '重试' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: '继续' })).not.toBeInTheDocument();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '重试' })));
    expect(send).toHaveBeenCalledWith('重试', { session_id: 'layout-test', kind: 'resume', expected_state_version: 0 });
  });

  it.each([
    ['chat.workflowContinueExecution', undefined],
    ['chat.workflowSkipThisApproval', 'step'],
    ['chat.workflowSkipFollowingApprovals', 'following'],
  ] as const)('keeps shared approval choice %s on the native path', async (label, scope) => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.display = { status: 'waiting', reason: 'approval_required', approval_step_id: 'write_document' };
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
    expect(send).toHaveBeenCalledWith(i18n.t('chat.workflowContinue'), {
      session_id: 'layout-test', kind: 'approve',
      step_id: 'write_document', expected_state_version: 0, ...(scope ? { preference_scope: scope } : {}),
    });
  });

  it('saves edits and approves the same checkpoint at the new version', async () => {
    fixture.session.status = 'waiting';
    fixture.session.state_version = 4;
    fixture.session.projection!.display = { status: 'waiting', reason: 'approval_required', approval_step_id: 'write_document' };
    fixture.flush.mockImplementationOnce(async () => {
      fixture.session = { ...fixture.session, state_version: 5 };
      return true;
    });
    const send = vi.fn();
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    fireEvent.click(await screen.findByRole('tab', { name: '成稿' }));
    await screen.findByRole('button', { name: '发布' });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '继续执行' })));
    expect(fixture.flush).toHaveBeenCalledOnce();
    expect(send).toHaveBeenCalledWith(i18n.t('chat.workflowContinue'), { session_id: 'layout-test', kind: 'approve', step_id: 'write_document', expected_state_version: 5 });
    expect(send).toHaveBeenCalledOnce();
  });

  it('keeps approval available when Chat refuses the control turn', async () => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.display = { status: 'waiting', reason: 'approval_required', approval_step_id: 'write_document' };
    const send = vi.fn(async () => false);
    render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    await screen.findByText('正文 document');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '继续执行' })));
    expect(send).toHaveBeenCalledOnce();
    expect(fixture.session.projection!.display.status).toBe('waiting');
    expect(screen.getByRole('button', { name: '继续执行' })).toBeEnabled();
  });
});


describe('external workflow surface boundary', () => {
  it.each(['pending', 'dispatching', 'accepted', 'unknown'])(
    'does not add a host delivery lock to native approval: %s', async status => {
      fixture.session.status = 'waiting';
      fixture.session.projection = {
        display: { status: 'waiting', reason: 'approval_required', approval_step_id: 'write_document' },
        current: [], ready: ['next'], nodes: {
          next: { execution: 'none', validity: 'effective', requires_approval: false, reachability: 'reachable', readiness: 'ready', branch: 'active' },
        },
      };
      const review = { id: 'review', step_id: 'write_document', execution_id: 'attempt', status: 'pending' as const, version: 1, manifest_hash: 'hash' };
      const control: WorkflowControlView = {
        protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 8,
        continuation: 'awaiting_user', reviews: [review], active_executions: 0, active_execution_ids: [],
        binding: { bound: true }, admission: { can_begin: true },
        delivery: { id: 'previous-notification', kind: 'continue', status }, available_actions: [],
      };
      const execute = vi.fn(async () => {});
      const view = render(<WorkflowPanel conversationId='layout-test' />);
      await screen.findByRole('button', { name: '发布' });
      const buttons = () => ['工作流会话操作', '工作流审批操作'].flatMap(name => within(screen.getByRole('group', { name })).getAllByRole('button'))
        .map(button => ({ label: button.textContent, disabled: (button as HTMLButtonElement).disabled, title: button.title }));
      const native = buttons();
      view.rerender(<WorkflowPanel conversationId='layout-test' embedded controlAdapter={panelAdapter(control, execute)} />);
      expect(buttons()).toEqual(native);
      expect(screen.getByRole('button', { name: '继续执行' })).toBeEnabled();
      fireEvent.click(screen.getByRole('button', { name: '继续执行' }));
      await waitFor(() => expect(execute).toHaveBeenCalledWith({ kind: 'confirm_and_continue', stepId: 'write_document', review }));
    },
  );

  it.each([false, true])('retries the failed execution while browsing another tab (paused=%s)', async paused => {
    fixture.session.status = paused ? 'stopped' : 'failed';
    fixture.session.steps![1].status = 'failed';
    fixture.session.projection = {
      display: { status: paused ? 'paused' : 'failed', reason: '', approval_step_id: '' },
      current: ['write_document'], nodes: {
        write_document: { execution: 'failed', validity: 'effective', requires_approval: false, reachability: 'reachable', readiness: 'not_applicable', branch: 'active' },
      },
    };
    const control: WorkflowControlView = {
      protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 8,
      continuation: paused ? 'stopped' : 'failed', reviews: [], active_executions: 0, active_execution_ids: [],
      binding: { bound: true }, admission: { can_begin: false }, delivery: null, available_actions: paused ? ['resume'] : ['retry'],
    };
    const send = vi.fn(async () => {});
    const execute = vi.fn(async () => {});
    const view = render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    fireEvent.click(await screen.findByRole('tab', { name: '写作准备' }));
    const retry = screen.getByRole('button', { name: '重试' });
    expect(retry).toBeEnabled();
    fireEvent.click(retry);
    await waitFor(() => expect(send).toHaveBeenCalledWith(i18n.t('chat.workflowRetry'), {
      session_id: 'layout-test', kind: paused ? 'resume' : 'retry', expected_state_version: 0,
    }));
    view.rerender(<WorkflowPanel conversationId='layout-test' embedded controlAdapter={panelAdapter(control, execute)} />);
    expect(screen.getByRole('tab', { name: '写作准备' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    await waitFor(() => expect(execute).toHaveBeenCalledWith(paused
      ? { kind: 'resume' } : { kind: 'retry', stepId: 'write_document' }));
  });

  it('does not approve a new attempt that appeared while saving external edits', async () => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.display = { status: 'waiting', reason: 'approval_required', approval_step_id: 'write_document' };
    const execute = vi.fn(async () => {});
    fixture.flush.mockImplementationOnce(async () => {
      fixture.session = { ...fixture.session, steps: fixture.session.steps!.map(step => ({ ...step, attempt: step.attempt + 1 })) };
      return true;
    });
    render(<WorkflowPanel conversationId='layout-test' embedded controlAdapter={{ execute }} />);
    await screen.findByRole('button', { name: '发布' });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: '继续执行' })));
    expect(fixture.flush).toHaveBeenCalledOnce();
    expect(execute).not.toHaveBeenCalled();
  });

  it.each(['automatic-gap', 'delivery-pending', 'review-without-checkpoint', 'interrupted', 'completed-follow-on'] as const)(
    'mirrors every native footer button: %s', async scenario => {
      const completed = scenario === 'completed-follow-on';
      const interrupted = scenario === 'interrupted';
      fixture.session.status = completed ? 'completed' : 'waiting';
      fixture.session.projection = {
        display: { status: completed ? 'completed' : interrupted ? 'paused' : 'running', reason: '', approval_step_id: '' },
        past: ['prepare'], current: interrupted ? ['write_document'] : [], completed,
        nodes: { write_document: {
          execution: interrupted ? 'interrupted' : 'succeeded', requires_approval: false,
          validity: 'effective', reachability: 'reachable', readiness: 'not_applicable', branch: 'active',
        } },
      };
      fixture.session.steps![1].status = interrupted ? 'interrupted' : 'succeeded';
      if (completed) fixture.ui.tabs![1].completed_continue_step = 'write_document';
      const control = {
        protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 64,
        continuation: completed ? 'completed' : scenario === 'review-without-checkpoint' ? 'awaiting_user' : 'continue',
        reviews: scenario === 'review-without-checkpoint' ? [{
          id: 'review', step_id: 'prepare', execution_id: 'old-attempt', status: 'pending', version: 1, manifest_hash: 'hash',
        }] : [],
        active_executions: 0, active_execution_ids: [], admission: { can_begin: false },
        binding: { bound: true, generation: 1 },
        delivery: scenario === 'delivery-pending' ? { kind: 'continue', status: 'accepted' } : null,
        available_actions: ['save', 'continue', 'resume', 'rewind', 'retry', 'confirm', 'confirm_and_continue'],
      } as WorkflowControlView;
      const view = render(<WorkflowPanel conversationId='layout-test' />);
      await screen.findByRole('button', { name: '发布' });
      const footerButtons = () => within(screen.getByRole('group', { name: '工作流会话操作' }))
        .getAllByRole('button').map(button => ({ label: button.textContent, disabled: (button as HTMLButtonElement).disabled }));
      const native = footerButtons();
      view.rerender(<WorkflowPanel conversationId='layout-test' embedded
        externalPresentation={{ activities: {}, compactEmptyStates: false }}
        controlAdapter={panelAdapter(control, vi.fn(async () => {}))} />);
      const external = footerButtons();
      expect(external).toEqual(native);
      // A running/preparing projection blocks continuation and approval on both
      // surfaces, even before an attempt exists or while capabilities refresh.
      expect(screen.queryByRole('group', { name: '工作流审批操作' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: '保存并继续' })).not.toBeInTheDocument();
      if (interrupted || completed) expect(screen.getByRole('button', { name: '继续' })).toBeEnabled();
    },
  );

  it.each(['preparing', 'executing', 'pausing'] as const)(
    'shares the native running controls during hosted %s, including before dispatch', async reason => {
      fixture.session.status = reason === 'pausing' ? 'pausing' : 'active';
      fixture.session.projection = { display: { status: 'running', reason, approval_step_id: '' } };
      fixture.session.steps = [];
      let finishStop!: () => void;
      const execute = vi.fn(() => new Promise<void>(resolve => { finishStop = resolve; }));
      const control: WorkflowControlView = {
        protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 8,
        continuation: 'continue', reviews: [], active_executions: reason === 'executing' ? 1 : 0,
        active_execution_ids: reason === 'executing' ? ['attempt'] : [],
        binding: { bound: true }, admission: { can_begin: reason === 'preparing' },
        delivery: reason === 'preparing' ? { id: 'start', kind: 'continue', status: 'pending' } : null,
        available_actions: ['stop', 'save', 'continue'],
      };
      const view = render(<WorkflowPanel conversationId='layout-test' />);
      await screen.findByText('正文 document');
      // Footer actions register in an effect after the document appears. Compare
      // both surfaces only once the native footer has finished registering.
      await screen.findByRole('button', { name: '发布' });
      const controls = () => within(screen.getByRole('group', { name: '工作流会话操作' }))
        .getAllByRole('button').map(button => ({ label: button.textContent, disabled: (button as HTMLButtonElement).disabled }));
      const native = controls();
      view.rerender(<WorkflowPanel conversationId='layout-test' embedded controlAdapter={panelAdapter(control, execute)} />);
      expect(controls()).toEqual(native);
      expect(screen.queryByRole('button', { name: '保存并继续' })).not.toBeInTheDocument();
      if (reason === 'pausing') {
        expect(screen.getByRole('button', { name: String(i18n.t('chat.workflowStopping')) })).toBeDisabled();
        return;
      }
      const stop = screen.getByRole('button', { name: '停止' });
      expect(stop).toBeEnabled();
      fireEvent.click(stop);
      await waitFor(() => expect(execute).toHaveBeenCalledWith({ kind: 'stop' }));
      fireEvent.click(screen.getByRole('button', { name: String(i18n.t('chat.workflowStopping')) }));
      expect(execute).toHaveBeenCalledOnce();
      expect(fixture.flush).not.toHaveBeenCalled();
      expect(fixture.pause).not.toHaveBeenCalled();
      await act(async () => finishStop());
    },
  );

  it('uses the native running footer and routes stop without saving editors', async () => {
    fixture.session.status = 'active';
    fixture.session.projection!.display = { status: 'running', reason: 'executing', approval_step_id: '' };
    fixture.session.steps![1].status = 'running';
    fixture.session.projection!.current = ['write_document'];
    const control = {
      protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 2,
      continuation: 'awaiting_executor', reviews: [], active_executions: 1, active_execution_ids: ['attempt-1'],
      admission: { can_begin: false }, binding: { bound: true, generation: 1 }, delivery: null,
      available_actions: ['stop'],
    } as WorkflowControlView;
    const execute = vi.fn(async () => {});
    render(<WorkflowPanel conversationId='layout-test' controlAdapter={panelAdapter(control, execute)} />);
    fireEvent.click(await screen.findByRole('button', { name: '停止' }));
    await waitFor(() => expect(execute).toHaveBeenCalledWith({ kind: 'stop' }));
    expect(fixture.flush).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
  });

  it('renders the same approval bar as native and does not expose rollback while awaiting review', async () => {
    fixture.session.status = 'waiting';
    fixture.session.projection!.display = { status: 'waiting', reason: 'approval_required', approval_step_id: 'write_document' };
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
    render(<WorkflowPanel conversationId='layout-test' controlAdapter={panelAdapter(control, execute)} />);
    const approval = await screen.findByRole('group', { name: '工作流审批操作' });
    expect(within(approval).getAllByRole('button').map(button => button.textContent)).toEqual([
      '继续执行', '此步骤不需审批', '以后此工作流无需审批',
    ]);
    expect(screen.queryByRole('group', { name: '回退到步骤：' })).not.toBeInTheDocument();
    fireEvent.click(within(approval).getByRole('button', { name: '此步骤不需审批' }));
    await waitFor(() => expect(execute).toHaveBeenCalledWith({
      kind: 'confirm_and_continue', stepId: 'write_document', review, preferenceScope: 'step',
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
      controlAdapter={panelAdapter(control, execute)} />);
    await screen.findByRole('button', { name: '发布' });
    const shortcuts = screen.getByRole('group', { name: '回退到步骤：' });
    expect(within(shortcuts).getByRole('button', { name: '成稿' })).toBeVisible();
    expect(screen.queryByRole('button', { name: '展开工作流面板' })).not.toBeInTheDocument();
    // Author styles such as display:flex can override the HTML hidden attribute.
    // A host without expansion support must not mount an inert button at all.
    expect(document.querySelector('.workflow-panel__expand-btn')).toBeNull();
    fireEvent.click(within(shortcuts).getByRole('button', { name: '成稿' }));
    await waitFor(() => expect(execute).toHaveBeenCalledWith({ kind: 'rewind', stepId: 'write_document' }));
    expect(fixture.flush).toHaveBeenCalledOnce();
    expect(fixture.flush.mock.invocationCallOrder[0]).toBeLessThan(execute.mock.invocationCallOrder[0]);
  });

  it('preserves native expansion and delegates embedded expansion only to a capable host', async () => {
    const toggle = vi.fn();
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('button', { name: '发布' });
    expect(view.container.querySelector('.workflow-panel__expand-btn')).not.toBeNull();
    view.rerender(<WorkflowPanel conversationId='layout-test' embedded
      externalPresentation={{ activities: {}, expanded: false, onToggleExpand: toggle }} />);
    fireEvent.click(view.container.querySelector<HTMLButtonElement>('.workflow-panel__expand-btn')!);
    expect(toggle).toHaveBeenCalledOnce();
    view.rerender(<WorkflowPanel conversationId='layout-test' embedded
      externalPresentation={{ activities: {} }} />);
    expect(view.container.querySelector('.workflow-panel__expand-btn')).toBeNull();
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
      controlAdapter={panelAdapter(control, execute)} />);
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
    const view = render(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {}, compactEmptyStates: true }} />);
    await screen.findByRole('tab', { name: /成稿/ });
    await waitFor(() => expect(screen.getByText('步骤执行失败')).toBeVisible());
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.queryByText('步骤执行失败')).not.toBeInTheDocument();
    expect(view.container.querySelector('.workflow-panel__slot-placeholder')).toBeNull();
  });

  it('auto-expands an empty external slot when content arrives and respects an explicit collapse', async () => {
    fixture.session.slots = [];
    fixture.ui.tabs![1].composite_behavior = { hide_empty_columns: false };
    fixture.ui.tabs![1].slots[0].widget = { widgetType: 'text-markdown', collapseWhenEmpty: true };
    const view = render(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {}, compactEmptyStates: true }} />);
    await screen.findByRole('tab', { name: /成稿/, selected: true });
    const button = () => view.container.querySelector<HTMLButtonElement>('.workflow-panel__slot-collapse')!;
    expect(button()).toHaveAttribute('aria-expanded', 'false');
    fixture.session.slots = [{ slot_id: 'document', slot: 'document', step_id: 'write_document', selected: true, revision: 1, artifact_value: { text: 'arrived' } }] as WorkflowSession['slots'];
    view.rerender(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {}, compactEmptyStates: true }} />);
    expect(button()).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('正文 document')).toBeVisible();
    fireEvent.click(button());
    view.rerender(<WorkflowPanel conversationId='layout-test' externalPresentation={{ activities: {}, compactEmptyStates: true }} />);
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
  await screen.findByRole('tab', { name: /成稿/, selected: true });
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
    reviews: [], binding: { bound: true }, delivery: null,
    admission: { can_begin: false }, available_actions: ['stop'],
  };
  const getControl = vi.fn(async () => ({ data: { data: {
    session: structuredClone(stored), control: structuredClone(control),
    display: { status: control.continuation === 'failed' ? 'failed' as const : 'running' as const, reason: '', approval_step_id: '' },
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
    controlAdapter={panelAdapter(snapshot.control!, commands.execute)} />;
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

describe('workflow follow and session presentation', () => {
  function enableFollowing() {
    fixture.ui = { ...fixture.ui, source_type: 'builtin', follow_mode: { default: 'following' } };
    fixture.session = { ...fixture.session, status: 'active', current_step_id: 'prepare', steps: [], projection: undefined };
  }
  const following = () => screen.getByRole('button', { name: '跟随进度' });
  const browsing = () => screen.getByRole('button', { name: '自由浏览' });

  it('freezes the current tab before any manual tab selection and resumes without execution', async () => {
    enableFollowing();
    const send = vi.fn();
    const view = render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    await screen.findByRole('button', { name: '跟随进度' });
    fireEvent.click(browsing());
    fixture.session = { ...fixture.session, current_step_id: 'write_document' };
    view.rerender(<WorkflowPanel conversationId='layout-test' onSendMessage={send} />);
    expect(screen.getByRole('tab', { name: '写作准备', selected: true })).toBeVisible();
    expect(screen.getByRole('tab', { name: /成稿.*当前/ })).toBeVisible();
    fireEvent.click(following());
    await screen.findByRole('tab', { name: '成稿', selected: true });
    expect(following()).toHaveAttribute('aria-pressed', 'true');
    expect(send).not.toHaveBeenCalled();
    expect(fixture.refresh).not.toHaveBeenCalled();
  });

  it('restores free browse and stable tab identity after insertion and remount', async () => {
    enableFollowing();
    fixture.ui.tabs!.unshift({ id: 'new', label: '新增步骤', slots: [], hide_when_material: 'skip_new' });
    fixture.ui.tab_visibility_ready_material = 'plan_ready';
    let view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('button', { name: '自由浏览' });
    fireEvent.click(screen.getByRole('tab', { name: '成稿' }));
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
    fixture.session = { ...fixture.session, slots: [...fixture.session.slots!,
      { slot: 'plan_ready', slot_id: 'plan_ready', selected: true, revision: 1 } as SlotRevision,
    ] };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(screen.getByRole('tab', { name: '新增步骤' })).toBeVisible();
    expect(screen.getByRole('tab', { name: '成稿', selected: true })).toBeVisible();
    view.unmount();
    fixture.focusedTab = undefined;
    view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
  });

  it('enters browse for editing and history and protects edits when resume saving fails', async () => {
    enableFollowing();
    fixture.session.current_step_id = 'write_document';
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('button', { name: '跟随进度' });
    fireEvent.click(screen.getByRole('button', { name: '查看历史' }));
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(following());
    await waitFor(() => expect(following()).toHaveAttribute('aria-pressed', 'true'));
    fireEvent.doubleClick(screen.getByText('正文 document'));
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
    fixture.session = { ...fixture.session, current_step_id: 'prepare' };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    fixture.flush.mockResolvedValue(false);
    fixture.flush.mockClear();
    fireEvent.click(following());
    await waitFor(() => expect(fixture.flush).toHaveBeenCalledOnce());
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('tab', { name: '成稿', selected: true })).toBeVisible();
    fixture.flush.mockResolvedValue(true);
    fireEvent.click(following());
    await screen.findByRole('tab', { name: '写作准备', selected: true });
  });

  it('ignores a late save after the user browses elsewhere or changes workflow runs', async () => {
    enableFollowing();
    fixture.session.current_step_id = 'write_document';
    const view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('button', { name: '自由浏览' });
    fireEvent.click(browsing());
    let complete!: (saved: boolean) => void;
    fixture.flush.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
    fireEvent.click(following());
    expect(following()).toBeDisabled();
    fireEvent.click(screen.getByRole('tab', { name: '写作准备' }));
    await act(async () => complete(true));
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('tab', { name: '写作准备', selected: true })).toBeVisible();
    expect(following()).toBeEnabled();
    fireEvent.click(screen.getByRole('tab', { name: /成稿/ }));
    fixture.flush.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
    fireEvent.click(following());
    fixture.session = { ...fixture.session, session_id: 'next-run', current_step_id: 'prepare' };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    fireEvent.click(await screen.findByRole('button', { name: '自由浏览' }));
    await act(async () => complete(true));
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('tab', { name: '写作准备', selected: true })).toBeVisible();
    expect(following()).toBeEnabled();
  });

  it('enters browse for artifact preview, stays free after close and exits preview on explicit follow', async () => {
    enableFollowing();
    fixture.session.current_step_id = 'write_document';
    function Host() {
      const controller = useArtifactPreviewController('layout-test');
      return <ArtifactPreviewContext.Provider value={controller}>
        <button onClick={() => controller.open({ key: 'plan:1', filename: 'plan.json', content: 'plan',
          workflow: { sessionId: 'layout-test', stepId: 'prepare' } })}>预览计划</button>
        <WorkflowPanel conversationId='layout-test' />
      </ArtifactPreviewContext.Provider>;
    }
    render(<Host />);
    await screen.findByRole('button', { name: '自由浏览' });
    fireEvent.click(screen.getByRole('button', { name: '预览计划' }));
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('tab', { name: '写作准备', selected: true })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: '关闭预览' }));
    expect(browsing()).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByRole('button', { name: '预览计划' }));
    fireEvent.click(following());
    await screen.findByRole('tab', { name: '成稿', selected: true });
    expect(screen.queryByRole('region', { name: 'plan.json' })).not.toBeInTheDocument();
  });

  it('expands any builtin and keeps shrinking only for the same session', async () => {
    fixture.ui.source_type = 'builtin';
    fixture.session.status = 'active';
    let view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('button', { name: '缩小工作流面板' });
    expect(screen.queryByRole('button', { name: '跟随进度' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '缩小工作流面板' }));
    fixture.session = { ...fixture.session, status: 'completed' };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    expect(document.querySelector('.workflow-panel--expanded')).toBeNull();
    view.unmount();
    view = render(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('tab', { name: '成稿', selected: true });
    expect(document.querySelector('.workflow-panel--expanded')).toBeNull();
    fixture.session = { ...fixture.session, session_id: 'new-run', status: 'active' };
    view.rerender(<WorkflowPanel conversationId='layout-test' />);
    await screen.findByRole('button', { name: '缩小工作流面板' });
  });
});


it.each([false, true])('preserves mainline no_advance controls without adding continuation (external=%s)', async external => {
  fixture.session.status = 'waiting';
  fixture.session.projection = {
    display: { status: 'failed', reason: 'no_advance', approval_step_id: '' },
    ready: ['next'], current: [], nodes: {
      write_document: { execution: 'succeeded', validity: 'effective', requires_approval: false, reachability: 'reachable', readiness: 'not_applicable', branch: 'active' },
      next: { execution: 'none', validity: 'effective', requires_approval: true, reachability: 'reachable', readiness: 'ready', branch: 'active' },
    },
  };
  const send = vi.fn(async (_message: string) => true);
  const execute = vi.fn(async () => {});
  render(<WorkflowPanel conversationId='layout-test' onSendMessage={send} controlAdapter={external ? { execute } : undefined} />);
  expect(await screen.findByRole('button', { name: '重试' })).toBeEnabled();
  expect(screen.queryByRole('button', { name: '保存并继续' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '继续' })).not.toBeInTheDocument();
  expect(screen.queryByRole('group', { name: '工作流审批操作' })).not.toBeInTheDocument();
  expect(send).not.toHaveBeenCalled();
  expect(execute).not.toHaveBeenCalled();
});

it.each(['awaiting_user', 'stopped'] as const)('uses the shared approval step despite host continuation %s', async continuation => {
  fixture.session.status = 'waiting';
  fixture.session.projection!.display = { status: 'waiting', reason: 'approval_required', approval_step_id: 'write_document' };
  const review = { id: 'review', step_id: 'write_document', execution_id: 'attempt', status: 'pending' as const, version: 3, manifest_hash: 'manifest' };
  const execute = vi.fn(async () => {});
  const control: WorkflowControlView = {
    protocol: 'workflow.control.v1', session_id: 'layout-test', state_version: 8,
    continuation, reviews: [{ ...review, id: 'other-review', step_id: 'prepare' }, review], active_executions: 0, active_execution_ids: [],
    binding: { bound: true }, admission: { can_begin: false }, delivery: null, available_actions: ['resume'],
  };
  render(<WorkflowPanel conversationId='layout-test' embedded controlAdapter={panelAdapter(control, execute)} />);
  fireEvent.click(await screen.findByRole('button', { name: '继续执行' }));
  await waitFor(() => expect(execute).toHaveBeenCalledOnce());
  expect(execute).toHaveBeenCalledWith({ kind: 'confirm_and_continue', stepId: 'write_document', review });
  expect(screen.queryByRole('button', { name: '继续' })).not.toBeInTheDocument();
});
