import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useImperativeHandle, useState, type ComponentProps } from 'react';
import type StateGraphEditor from '../../components/StateGraphEditor';
import type { WorkflowDraftRecord } from '../../workflowDraftApi';
import WorkflowDetailPage from './index';

const mocks = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), flush: vi.fn(), publish: vi.fn(), generate: vi.fn(), repair: vi.fn(), cancelRepair: vi.fn(), preview: vi.fn(), repairRun: vi.fn(), versions: vi.fn(), version: vi.fn(), props: {} as ComponentProps<typeof StateGraphEditor>, t: (key: string) => key }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('react-router-dom', () => ({ useParams: () => ({ workflowId: 'draft-test' }), useNavigate: () => vi.fn(), useOutletContext: () => ({}) }));
vi.mock('@/components/request', () => ({ localizeErrorCode: () => 'Request failed' }));
vi.mock('../../components/LinkedSkillButton', () => ({ default: () => null }));
vi.mock('../../components/StateGraphEditor', () => ({ default: (props: ComponentProps<typeof StateGraphEditor>) => {
  mocks.props = props;
  useImperativeHandle(props.editorRef, () => ({ flushPendingChanges: mocks.flush }));
  const [mountedState] = useState(props.initialStateYaml);
  return <div role="region" aria-label="Editor" data-state={mountedState} data-readonly={props.readonly}>{props.workflowName}{props.topbarExtra}{props.topbarActions}</div>;
} }));
vi.mock('../../workflowDraftApi', () => ({
  getWorkflowDraft: mocks.get, updateWorkflowDraftContent: mocks.save, aiGenerateWorkflowDraft: mocks.generate,
  repairWorkflowDraft: mocks.repair, previewWorkflowRepair: mocks.preview, getWorkflowRepairRun: mocks.repairRun,
  cancelWorkflowDraftRepair: mocks.cancelRepair,
  publishWorkflowDraft: mocks.publish,
  getWorkflowGenerationAnalysis: vi.fn(async () => null),
  listWorkflowDrafts: vi.fn(async () => ({ records: [], total: 0 })), listWorkflowVersions: mocks.versions, getWorkflowVersion: mocks.version,
}));

const draft = {
  id: 'draft-test', name: 'My research workflow', version: 3, source_type: 'ai', generate_status: 'done',
  workflow_yaml_content: 'id: research-v2\nname: My research workflow\nsteps: [{id: research}]\nslots: [{id: report}]',
  state_yaml_content: 'steps: [{id: research}]\ntransitions: {__start__: [{to: research}], research: [{to: __end__}]}',
  design_brief_content: 'Research brief', scenario_content: 'Research documentation',
} as WorkflowDraftRecord;
function show(value: Partial<WorkflowDraftRecord> = {}) {
  mocks.get.mockResolvedValue({ ...draft, ...value });
  return render(<ConfigProvider theme={{ token: { motion: false } }}><WorkflowDetailPage /></ConfigProvider>);
}
beforeEach(() => {
  mocks.save.mockReset(); mocks.publish.mockReset(); mocks.generate.mockReset(); mocks.repair.mockReset(); mocks.repairRun.mockReset();
  mocks.flush.mockReset().mockResolvedValue(true);
  mocks.cancelRepair.mockReset();
  mocks.versions.mockReset().mockResolvedValue([]); mocks.version.mockReset();
  mocks.preview.mockReset().mockResolvedValue({ target: 'node_integration', mode: 'workflow_local', draft_version: 4, diagnostics: [], planned_files: ['workflow.yaml', 'scenario/state.yml', 'scenario/scenario.md'] });
  localStorage.clear();
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('workflow draft lifecycle', () => {
  it('flushes editor changes and waits for the shared save queue before publishing once', async () => {
    show();
    const publish = await screen.findByRole('button', { name: '发布插件' });
    let finishFlush!: (saved: boolean) => void;
    let finishSave!: (saved: WorkflowDraftRecord) => void;
    let finishPublish!: (result: unknown) => void;
    mocks.flush.mockImplementation(() => new Promise((resolve) => { finishFlush = resolve; }));
    mocks.save.mockImplementation(() => new Promise((resolve) => { finishSave = resolve; }));
    mocks.publish.mockImplementation(() => new Promise((resolve) => { finishPublish = resolve; }));
    let pendingSave!: Promise<void>;
    await act(async () => {
      pendingSave = mocks.props.onSave!({ stateYaml: 'latest graph', workflowYaml: 'id: latest', scenarioContent: 'latest instructions', scriptsContent: '{}', stateLayoutContent: '{}' });
    });
    fireEvent.click(publish);
    fireEvent.click(publish);
    expect(mocks.flush).toHaveBeenCalledTimes(1);
    expect(mocks.props.readonly).toBe(true);
    expect(mocks.publish).not.toHaveBeenCalled();
    await act(async () => { finishFlush(true); });
    expect(mocks.publish).not.toHaveBeenCalled();
    await act(async () => { finishSave({ ...draft, version: 4 }); await pendingSave; });
    expect(mocks.publish).toHaveBeenCalledTimes(1);
    expect(mocks.props.readonly).toBe(true);
    await act(async () => { finishPublish({ workflow_ref: 'user:owner:research-v2', revision_no: 2 }); });
    await waitFor(() => expect(mocks.props.readonly).toBe(false));
  });

  it('does not publish when the editor cannot flush the current draft', async () => {
    show();
    mocks.flush.mockResolvedValue(false);
    fireEvent.click(await screen.findByRole('button', { name: '发布插件' }));
    await waitFor(() => expect(mocks.props.readonly).toBe(false));
    expect(mocks.flush).toHaveBeenCalledTimes(1);
    expect(mocks.publish).not.toHaveBeenCalled();
    expect(mocks.props.initialStateYaml).toBe(draft.state_yaml_content);
  });

  it('does not publish when a queued metadata save fails', async () => {
    show();
    fireEvent.click(await screen.findByRole('button', { name: draft.name }));
    let rejectSave!: (error: Error) => void;
    mocks.save.mockImplementation(() => new Promise((_, reject) => { rejectSave = reject; }));
    const input = screen.getByDisplayValue(draft.name);
    fireEvent.change(input, { target: { value: 'Rename pending' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: '发布插件' }));
    await act(async () => { rejectSave(new Error('save failed')); });
    await waitFor(() => expect(mocks.props.readonly).toBe(false));
    expect(mocks.publish).not.toHaveBeenCalled();
  });

  it('retries a failed preview in place without losing the hint or submitting a repair', async () => {
    mocks.preview.mockRejectedValueOnce(new Error('temporary network failure'));
    show();
    await screen.findByRole('region', { name: 'Editor' });
    await act(async () => { mocks.props.onRepair?.('node_integration', [], { node_ids: ['review'] }); });
    const hint = screen.getByPlaceholderText('selfEvolutionRun.workflowNodeIntegrationPlaceholder');
    fireEvent.change(hint, { target: { value: 'Review before export; keep the output length' } });
    const retry = await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairPreviewRetry' });
    expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })).toBeDisabled();
    expect(mocks.preview).toHaveBeenCalledTimes(1);
    fireEvent.click(retry);
    await waitFor(() => expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })).toBeEnabled());
    expect(hint).toHaveValue('Review before export; keep the output length');
    expect(mocks.preview).toHaveBeenCalledTimes(2);
    expect(mocks.repair).not.toHaveBeenCalled();
  });

  it('previews a newly selected target after a preview failure', async () => {
    mocks.preview.mockRejectedValueOnce(new Error('temporary network failure'));
    show();
    await screen.findByRole('region', { name: 'Editor' });
    await act(async () => { mocks.props.onRepair?.('node_integration'); });
    await screen.findByText('selfEvolutionRun.workflowDetailRepairPreviewFailed');
    const dialog = screen.getByRole('dialog', { name: 'selfEvolutionRun.workflowDetailRepairModalTitle' });
    fireEvent.mouseDown(within(dialog).getByRole('combobox'));
    fireEvent.click(await screen.findByText('界面展示', { selector: '.ant-select-item-option-content' }));
    await waitFor(() => expect(mocks.preview).toHaveBeenLastCalledWith(draft.id, expect.objectContaining({ target: 'ui', draft_version: 3 })));
    await waitFor(() => expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })).toBeEnabled());
    expect(screen.queryByText('selfEvolutionRun.workflowDetailRepairPreviewFailed')).not.toBeInTheDocument();
  });

  it('ignores an older preview response after switching repair targets', async () => {
    let rejectOldPreview!: (error: Error) => void;
    mocks.preview.mockImplementationOnce(() => new Promise((_, reject) => { rejectOldPreview = reject; }));
    show();
    await screen.findByRole('region', { name: 'Editor' });
    await act(async () => { mocks.props.onRepair?.('node_integration'); });
    const dialog = screen.getByRole('dialog', { name: 'selfEvolutionRun.workflowDetailRepairModalTitle' });
    fireEvent.mouseDown(within(dialog).getByRole('combobox'));
    fireEvent.click(await screen.findByText('界面展示', { selector: '.ant-select-item-option-content' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })).toBeEnabled());
    await act(async () => { rejectOldPreview(new Error('old scope network failure')); });
    expect(screen.queryByText('selfEvolutionRun.workflowDetailRepairPreviewFailed')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })).toBeEnabled();
    expect(mocks.preview).toHaveBeenCalledTimes(2);
  });

  it('keeps a preview version conflict blocked instead of retrying or adopting another draft', async () => {
    mocks.preview.mockRejectedValueOnce({ response: { status: 409 } });
    show();
    await screen.findByRole('region', { name: 'Editor' });
    await act(async () => { mocks.props.onRepair?.('node_integration'); });
    await screen.findByText('selfEvolutionRun.workflowDetailSaveConflict');
    expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairPreviewRetry' })).not.toBeInTheDocument();
    expect(mocks.preview).toHaveBeenCalledTimes(1);
    expect(mocks.props.initialStateYaml).toBe(draft.state_yaml_content);
  });

  async function openRepairProgress() {
    fireEvent.click(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailGenerationView' }));
    return screen.findByRole('dialog', { name: 'selfEvolutionRun.workflowDetailRepairModalTitle' });
  }

  async function openRepairCloseChoice() {
    const progress = await openRepairProgress();
    fireEvent.click(within(progress).getByRole('button', { name: 'Close' }));
    // The test setup assigns the same generated title id to every Ant Modal.
    const title = await screen.findByText('selfEvolutionRun.workflowDetailRepairCloseTitle');
    return title.closest('[role="dialog"]') as HTMLElement;
  }

  it('offers background or terminate after closing repair and keeps polling in the background', async () => {
    const intervals = vi.spyOn(globalThis, 'setInterval');
    show({ generate_status: 'repairing' });
    const choice = await openRepairCloseChoice();
    expect(within(choice).getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairStop' })).toBeEnabled();
    fireEvent.click(within(choice).getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairBackground' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByRole('status')).toHaveTextContent('selfEvolutionRun.workflowDetailRepairBackgroundTitle');
    expect(mocks.props.readonly).toBe(true);
    expect(mocks.cancelRepair).not.toHaveBeenCalled();
    expect(mocks.repair).not.toHaveBeenCalled();
    mocks.get.mockResolvedValue({ ...draft, version: 4, last_repair_run_id: 'repair-1' });
    mocks.repairRun.mockResolvedValue({ status: 'succeeded', target: 'node_integration', diagnostics_after: [] });
    const poll = intervals.mock.calls.filter(([, delay]) => delay === 3000).slice(-1)[0]?.[0];
    if (typeof poll !== 'function') throw new Error('Repair polling did not start');
    await act(async () => { await poll(); });
    await waitFor(() => expect(mocks.props.readonly).toBe(false));
    expect(mocks.repairRun).toHaveBeenCalledWith(draft.id, 'repair-1');
    expect(screen.queryByText('selfEvolutionRun.workflowDetailRepairBackgroundTitle')).not.toBeInTheDocument();
  });

  it('terminates repair, preserves the draft, and ignores a pre-cancel poll response', async () => {
    const intervals = vi.spyOn(globalThis, 'setInterval');
    show({ generate_status: 'repairing' });
    const choice = await openRepairCloseChoice();
    let resolvePoll!: (value: WorkflowDraftRecord) => void;
    mocks.get.mockImplementationOnce(() => new Promise((resolve) => { resolvePoll = resolve; }));
    const poll = intervals.mock.calls.filter(([, delay]) => delay === 3000).slice(-1)[0]?.[0];
    if (typeof poll !== 'function') throw new Error('Repair polling did not start');
    let pendingPoll!: Promise<unknown>;
    await act(async () => { pendingPoll = Promise.resolve(poll()); });
    mocks.cancelRepair.mockResolvedValue({ ...draft, version: 4 });
    fireEvent.click(within(choice).getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairStop' }));
    await waitFor(() => expect(mocks.cancelRepair).toHaveBeenCalledWith(draft.id));
    await waitFor(() => expect(mocks.props.readonly).toBe(false));
    await act(async () => { resolvePoll({ ...draft, generate_status: 'repairing' }); await pendingPoll; });
    expect(mocks.props.readonly).toBe(false);
    expect(mocks.props.initialStateYaml).toBe(draft.state_yaml_content);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByText('selfEvolutionRun.workflowDetailRepairBackgroundTitle')).not.toBeInTheDocument();
  });

  it('keeps repair active and the choice dialog available when termination fails', async () => {
    show({ generate_status: 'repairing' });
    const choice = await openRepairCloseChoice();
    mocks.cancelRepair.mockRejectedValue(new Error('network unavailable'));
    fireEvent.click(within(choice).getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairStop' }));
    await waitFor(() => expect(mocks.cancelRepair).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(choice).getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairStop' })).toBeEnabled());
    expect(choice).toBeVisible();
    expect(mocks.props.readonly).toBe(true);
  });

  it('can background a pending repair submission and restores the form if admission fails', async () => {
    show();
    await screen.findByRole('region', { name: 'Editor' });
    await act(async () => { mocks.props.onRepair?.('node_integration', [], { node_ids: ['review'] }); });
    let rejectRepair!: (reason: unknown) => void;
    mocks.repair.mockImplementation(() => new Promise((_, reject) => { rejectRepair = reject; }));
    const submit = screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' });
    await waitFor(() => expect(submit).toBeEnabled());
    fireEvent.click(submit);
    const progress = screen.getByRole('dialog', { name: 'selfEvolutionRun.workflowDetailRepairModalTitle' });
    fireEvent.click(within(progress).getByRole('button', { name: 'Close' }));
    const title = await screen.findByText('selfEvolutionRun.workflowDetailRepairCloseTitle');
    const choice = title.closest('[role="dialog"]') as HTMLElement;
    expect(within(choice).getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairStop' })).toBeDisabled();
    fireEvent.click(within(choice).getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairBackground' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(mocks.props.readonly).toBe(true);
    expect(screen.getByRole('button', { name: '发布插件' })).toBeDisabled();
    await act(async () => { rejectRepair({ response: { data: { message: 'Admission failed' } } }); });
    await waitFor(() => expect(screen.getByText('Admission failed')).toBeVisible());
    expect(screen.getByRole('dialog', { name: 'selfEvolutionRun.workflowDetailRepairModalTitle' })).toBeVisible();
    expect(mocks.cancelRepair).not.toHaveBeenCalled();
  });

  it('mounts the history graph only with its loaded revision and ignores stale responses', async () => {
    mocks.versions.mockResolvedValue([{ revision_id: 'r1', revision_no: 1 }, { revision_id: 'r2', revision_no: 2 }]);
    show({ published_workflow_ref: 'user:owner:research-v2', draft_dirty: false });
    const editor = await screen.findByRole('region', { name: 'Editor' });
    let resolveFirst!: (value: unknown) => void;
    let resolveSecond!: (value: unknown) => void;
    mocks.version.mockImplementation((_ref, revision) => new Promise((resolve) => {
      if (revision === 'r1') resolveFirst = resolve; else resolveSecond = resolve;
    }));
    const selectVersion = async (label: string) => {
      fireEvent.mouseDown(screen.getByRole('combobox'));
      fireEvent.click(await screen.findByText(label, { selector: '.ant-select-item-option-content' }));
    };
    await selectVersion('v1');
    expect(editor).toHaveAttribute('data-state', draft.state_yaml_content);
    expect(editor).toHaveAttribute('data-readonly', 'true');
    await selectVersion('v2');
    await act(async () => resolveSecond({ revision_id: 'r2', state_yaml_content: 'v2 graph', workflow_yaml_content: 'id: v2' }));
    expect(screen.getByRole('region', { name: 'Editor' })).toHaveAttribute('data-state', 'v2 graph');
    await act(async () => resolveFirst({ revision_id: 'r1', state_yaml_content: 'v1 graph' }));
    expect(screen.getByRole('region', { name: 'Editor' })).toHaveAttribute('data-state', 'v2 graph');
    await selectVersion('v1');
    await selectVersion('草稿');
    await act(async () => resolveFirst({ revision_id: 'r1', state_yaml_content: 'late graph' }));
    expect(screen.getByRole('region', { name: 'Editor' })).toHaveAttribute('data-state', draft.state_yaml_content);
    expect(mocks.props.readonly).toBe(false);
  });

  it.each(['succeeded', 'failed'] as const)('only restores publishing after a validated repair (%s) of a failed Skill conversion', async (status) => {
    const intervals = vi.spyOn(globalThis, 'setInterval');
    show({ source_type: 'skill', generate_status: 'failed', generate_error: 'generation validation failed' });
    await screen.findByRole('region', { name: 'Editor' });
    expect(screen.queryByRole('button', { name: '发布插件' })).not.toBeInTheDocument();
    await act(async () => { mocks.props.onRepair?.('node_integration', [], { node_ids: ['research'] }); });
    mocks.repair.mockResolvedValue({ ...draft, source_type: 'skill', generate_status: 'repairing' });
    mocks.repairRun.mockResolvedValue({ status, target: 'node_integration', diagnostics_after: status === 'succeeded' ? [] : [{ code: 'E_UI_EMPTY', severity: 'error', message: 'UI tabs required' }] });
    const repaired = { ...draft, source_type: 'skill', version: 4, last_repair_run_id: 'repair-1', generate_status: status === 'succeeded' ? 'done' : 'failed', generate_error: status === 'succeeded' ? '' : 'generation validation failed' };
    mocks.get.mockResolvedValue(repaired);
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })); });
    const poll = intervals.mock.calls.filter(([, delay]) => delay === 3000).slice(-1)[0]?.[0];
    if (typeof poll !== 'function') throw new Error('Repair polling did not start');
    await act(async () => { await poll(); });
    expect(mocks.repairRun).toHaveBeenCalledWith(draft.id, 'repair-1');
    await waitFor(() => expect(screen.getByRole('region', { name: 'Editor' })).toHaveAttribute('data-readonly', 'false'));
    if (status === 'failed') {
      expect(screen.queryByRole('button', { name: '发布插件' })).not.toBeInTheDocument();
      expect(mocks.publish).not.toHaveBeenCalled();
      return;
    }
    expect(screen.queryByRole('button', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' })).not.toBeInTheDocument();
    const publish = screen.getByRole('button', { name: '发布插件' });
    expect(publish).toBeEnabled();
    mocks.publish.mockResolvedValue({ workflow_ref: 'user:owner:research-v2', revision_no: 1 });
    mocks.get.mockResolvedValue({ ...repaired, published: true, current_revision_no: 1, draft_dirty: false });
    fireEvent.click(publish);
    await waitFor(() => expect(mocks.publish).toHaveBeenCalledWith(draft.id));
    expect(await screen.findByText('线上：v1')).toBeVisible();
    expect(screen.getByRole('button', { name: '发布插件' })).toBeDisabled();
  });

  it.each(['analyzing', 'generating', 'brief_done', 'skeleton_done', 'state_done', 'repairing', 'needs_confirmation'])('prevents edits during %s', async (generate_status) => {
    show({ generate_status });
    expect(await screen.findByRole('region', { name: 'Editor' })).toHaveAttribute('data-readonly', 'true');
  });

  it('repairs the latest saved version using current workflow intent rather than the source Skill', async () => {
    show({ source_analysis_id: 'original-skill-analysis' });
    await screen.findByRole('region', { name: 'Editor' });
    mocks.save.mockResolvedValue({ ...draft, source_analysis_id: 'original-skill-analysis', version: 4 });
    const payload = { stateYaml: 'steps: [{id: review}]', workflowYaml: 'id: research-v2', scenarioContent: 'Review before export', scriptsContent: '{}', stateLayoutContent: '{}' };
    await act(async () => { await mocks.props.onSave?.(payload); });
    await act(async () => { mocks.props.onRepair?.('node_integration', [], { node_ids: ['review'] }); });
    expect(mocks.preview).toHaveBeenCalledWith(draft.id, { target: 'node_integration', mode: 'workflow_local', draft_version: 4 });
    fireEvent.change(screen.getByPlaceholderText('selfEvolutionRun.workflowNodeIntegrationPlaceholder'), { target: { value: 'Review before export' } });
    mocks.repair.mockResolvedValue({ ...draft, version: 4, generate_status: 'repairing' });
    fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' }));
    await waitFor(() => expect(mocks.repair).toHaveBeenCalledWith(draft.id, expect.objectContaining({
      target: 'node_integration', mode: 'workflow_local', draft_version: 4,
      repair_hint: 'Review before export', edit_context: { node_ids: ['review'] },
    })));
  });

  it('retains the user hint and allows retry after repair admission rejects incomplete intent', async () => {
    show();
    await screen.findByRole('region', { name: 'Editor' });
    await act(async () => { mocks.props.onRepair?.('node_integration', [], { node_ids: ['review'] }); });
    const hint = screen.getByPlaceholderText('selfEvolutionRun.workflowNodeIntegrationPlaceholder');
    fireEvent.change(hint, { target: { value: 'Review report' } });
    mocks.repair.mockRejectedValue({ response: { data: { message: 'Please specify the insertion point' } } });
    fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' }));
    await waitFor(() => expect(screen.getByText('Please specify the insertion point')).toBeVisible());
    expect(screen.getByPlaceholderText('selfEvolutionRun.workflowNodeIntegrationPlaceholder')).toHaveValue('Review report');
    expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })).toBeEnabled();
  });

  it.each(['statemachine', 'full'] as const)('keeps %s repair local after reopening a completed Skill workflow', async (target) => {
    show({ source_type: 'skill', source_analysis_id: 'original-analysis' });
    await screen.findByRole('region', { name: 'Editor' });
    // A freshly mounted editor has no in-session added nodes or placeholders.
    await act(async () => { mocks.props.onRepair?.(target, [{ code: 'E_MATERIAL_UNKNOWN', nodeId: 'research', message: 'Fix input' }], { node_ids: [] }); });
    expect(mocks.preview).toHaveBeenCalledWith(draft.id, { target: 'node_integration', mode: 'workflow_local', draft_version: 3 });
    mocks.repair.mockResolvedValue({ ...draft, source_type: 'skill', version: 3, generate_status: 'repairing' });
    fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' }));
    await waitFor(() => expect(mocks.repair).toHaveBeenCalledWith(draft.id, expect.objectContaining({ target: 'node_integration', mode: 'workflow_local', edit_context: { node_ids: [] } })));
  });

  it('restores actionable repair diagnostics after reopening a failed draft', async () => {
    mocks.repairRun.mockResolvedValue({ status: 'failed', diagnostics_after: [{ code: 'E_REPAIR_MODE_CHANGED', severity: 'error', node_id: 'research', message: 'Keep human review' }] });
    show({ last_repair_run_id: 'repair-1', generate_warning: '[修复失败] Keep human review' });
    await waitFor(() => expect(mocks.props.repairDiagnostics).toEqual([expect.objectContaining({ code: 'E_REPAIR_MODE_CHANGED', nodeId: 'research' })]));
    expect(mocks.props.readonly).toBe(false);
  });

  it('does not adopt a newer version and overwrite it after a repair conflict', async () => {
    show();
    await screen.findByRole('region', { name: 'Editor' });
    await act(async () => { mocks.props.onRepair?.('node_integration', [], { node_ids: ['review'] }); });
    mocks.repair.mockRejectedValue({ response: { status: 409, data: { message: 'repair stale draft' } } });
    mocks.get.mockResolvedValue({ ...draft, version: 5, scenario_content: 'Another editor changed this' });
    fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' }));
    await waitFor(() => expect(screen.getByText('selfEvolutionRun.workflowDetailSaveConflict')).toBeVisible());
    expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRepairSubmit' })).toBeDisabled();
    await expect(mocks.props.onSave?.({ stateYaml: 'old graph', workflowYaml: 'id: old', scenarioContent: 'old description', scriptsContent: '{}', stateLayoutContent: '{}' })).rejects.toBeTruthy();
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.props.initialScenarioContent).toBe(draft.scenario_content);
  });

  it('shows the same display name before and during editing and persists a rename', async () => {
    show();
    const button = await screen.findByRole('button', { name: draft.name });
    fireEvent.click(button);
    const input = screen.getByDisplayValue(draft.name);
    mocks.save.mockResolvedValue({ ...draft, name: 'Renamed research', version: 4 });
    fireEvent.change(input, { target: { value: 'Renamed research' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' });
    await waitFor(() => expect(mocks.save).toHaveBeenCalledWith(draft.id, { name: 'Renamed research', version: 3 }));
    expect(await screen.findByRole('button', { name: 'Renamed research' })).toBeInTheDocument();
  });

  it('renders structured generation failures instead of crashing the recovery page', async () => {
    show({ generate_status: 'failed', generate_error: JSON.stringify({ phase: 'state_machine', code: 'GENERATION_CANCELED', message: 'Stopped by user', suggestions: ['Retry from the last checkpoint.'] }) });
    const details = await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' });
    expect(details).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('GENERATION_CANCELED')).not.toBeVisible();
    fireEvent.click(details);
    expect(details).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('Retry from the last checkpoint.')).toBeVisible();
    expect(screen.getByText('Stopped by user')).toBeVisible();
    fireEvent.click(details);
    expect(screen.getByText('GENERATION_CANCELED')).not.toBeVisible();
    expect(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailRegenerate' })).toBeEnabled();
  });

  it('keeps recovery available without error details and only starts generation after confirmation', async () => {
    show({ generate_status: 'failed', generate_error: '' });
    fireEvent.click(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailRegenerate' }));
    expect(screen.queryByRole('button', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('dialog', { name: '选择重新生成起点' })).toBeVisible());
    expect(mocks.generate).not.toHaveBeenCalled();
  });

  it('keeps the required-action hint visible for failures that cannot be retried immediately', async () => {
    show({ generate_status: 'failed', generate_error: JSON.stringify({ phase: 'validation', recoverable: false, message: 'Invalid workflow configuration' }) });
    expect(await screen.findByText('selfEvolutionRun.workflowDetailFailureNeedsAttention')).toBeVisible();
    expect(screen.getByText('Invalid workflow configuration')).not.toBeVisible();
  });

  it('dismisses only the failure notice and preserves the editor and its error log', async () => {
    show({ generate_status: 'failed', generate_error: 'phase-1 analysis: upstream error' });
    fireEvent.click(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailDismissFailure' }));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Editor' })).toBeVisible();
    expect(mocks.props.generationLog).toBe('phase-1 analysis: upstream error');
    expect(JSON.parse(localStorage.getItem('workflow_banners_dismissed:draft-test')!)).toContain('failed');
  });

  it('keeps optional generation advice collapsed and preserves the original log', async () => {
    const warning = 'phase3 scenario_scripts failed: temporary provider error';
    show({ generate_warning: warning });
    const details = await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailShowSuggestions' });
    expect(screen.getByRole('status')).toHaveTextContent('selfEvolutionRun.workflowDetailCompletedTitle');
    expect(details).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('selfEvolutionRun.workflowDetailSuggestionsHint')).toBeVisible();
    expect(screen.getByText('temporary provider error')).not.toBeVisible();
    expect(screen.queryByText('Request failed')).not.toBeInTheDocument();
    fireEvent.click(details);
    expect(details).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('region', { name: 'selfEvolutionRun.workflowDetailShowSuggestions' })).toHaveAttribute('id', details.getAttribute('aria-controls'));
    expect(screen.getByText('temporary provider error')).toBeVisible();
    fireEvent.click(details);
    expect(screen.getByText('temporary provider error')).not.toBeVisible();
    expect(mocks.props.generationLog).toContain(warning);
    expect(mocks.generate).not.toHaveBeenCalled();
  });

  it('persists dismissals for the same advice but shows new advice', async () => {
    const first = show({ generate_warning: 'First warning' });
    fireEvent.click(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailDismissNotice' }));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Editor' })).toBeVisible();
    expect(mocks.props.generationLog).toContain('First warning');
    first.unmount();
    const second = show({ generate_warning: 'First warning' });
    await screen.findByRole('region', { name: 'Editor' });
    expect(screen.queryByRole('button', { name: 'selfEvolutionRun.workflowDetailShowSuggestions' })).not.toBeInTheDocument();
    second.unmount();
    show({ generate_warning: 'A different warning' });
    expect(await screen.findByRole('button', { name: 'selfEvolutionRun.workflowDetailShowSuggestions' })).toHaveAttribute('aria-expanded', 'false');
  });

  it('keeps repair failures distinct from optional generation advice', async () => {
    show({ generate_warning: '[修复失败] temporary provider error' });
    expect(await screen.findByRole('alert')).toHaveTextContent('selfEvolutionRun.workflowDetailRepairFailedBanner');
    expect(screen.queryByText('selfEvolutionRun.workflowDetailCompletedTitle')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' }));
    expect(screen.getByRole('region', { name: 'selfEvolutionRun.workflowDetailShowFailureDetails' })).toBeVisible();
  });

});
