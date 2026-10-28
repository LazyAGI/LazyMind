import { createRef, useImperativeHandle } from 'react';
import type { ComponentProps } from 'react';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type GraphCanvas from './GraphCanvas';
import type ArtifactPanel from './ArtifactPanel';
import type YamlEditor from './YamlEditor';
import type UiEditorPanel from './UiEditorPanel';
import StateGraphEditor, { type StateGraphEditorHandle } from './index';

const canvas = vi.hoisted(() => ({
  addNode: vi.fn(),
  focusNode: vi.fn(() => true),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('@/utils/developerMode', () => ({ isDeveloperModeActive: () => false }));

vi.mock('./GraphCanvas', () => ({
  default: function Canvas({ canvasRef, readonly, model, onModelChange }: ComponentProps<typeof GraphCanvas>) {
    useImperativeHandle(canvasRef, () => ({ ...canvas, addNode: () => {
      canvas.addNode();
      onModelChange({ ...model, nodes: [...model.nodes, {
        id: `review_${model.nodes.length}`, label: 'Review report', mode: 'human',
        inputs: [{ material: 'report', required: true }], outputs: [], transitions: [],
      }] });
    } }));
    return <div role="region" aria-label="Graph canvas" data-readonly={readonly} />;
  },
}));

vi.mock('./ArtifactPanel', () => ({
  default: ({ onClose, workflowModel, readonly }: ComponentProps<typeof ArtifactPanel>) => (
    <aside aria-label="Artifacts" data-readonly={readonly}>
      <span>{workflowModel?.name}</span>
      <span>{workflowModel?.ui?.tabs.map((tab) => tab.label).join(', ')}</span>
      <button type="button" onClick={onClose}>Close artifacts</button>
    </aside>
  ),
}));

vi.mock('./YamlEditor', () => ({
  default: ({ value, onChange, readOnly, language }: ComponentProps<typeof YamlEditor>) => (
    <textarea
      aria-label="Code editor"
      value={value}
      readOnly={readOnly}
      data-language={language}
      onChange={(event) => onChange(event.target.value)}
    />
  ),
}));

vi.mock('./UiEditorPanel', () => ({
  default: ({ workflowModel, readonly }: ComponentProps<typeof UiEditorPanel>) => (
    <div role="region" aria-label="UI editor" data-readonly={readonly}>
      {workflowModel.name}
    </div>
  ),
}));

vi.mock('./WorkflowInfoModal', () => ({ default: () => null }));

const stateYaml = `
steps:
  - id: gather
    label: Gather sources
    mode: auto
    outputs: [{material: source_notes}]
  - id: write
    label: Write report
    mode: auto
    inputs: [{material: source_notes, required: true}]
    outputs: [{material: report}]
transitions:
  __start__: [{to: gather}]
  gather: [{to: write}]
  write: [{to: __end__}]
`;

const workflowYaml = `
id: report-workflow
name: Report workflow
steps:
  - {id: gather, label: Gather sources}
  - {id: write, label: Write report}
slots:
  - {id: source_notes, label: Source notes, type: text}
  - {id: report, label: Final report, type: text}
ui:
  tabs:
    - id: result
      label: Result page
      layout: vertical
      slots: [{id: report}]
`;

const initialProps = {
  initialStateYaml: stateYaml,
  initialWorkflowYaml: workflowYaml,
  initialScenarioContent: '## 场景描述\n\nPrepare a source-backed report.',
};

const button = (key: string) => screen.getByRole('button', { name: new RegExp(`selfEvolutionRun\\.${key}$`) });
const artifactsButton = () => screen.getByRole('button', { name: /selfEvolutionRun.sgeArtifactsBtn/ });

async function flushEditorWork() {
  await act(async () => { await vi.advanceTimersByTimeAsync(2200); });
}

describe('StateGraphEditor workspace navigation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('opens artifacts by default, supplies page metadata, and reports visibility changes', () => {
    const onArtifactsChange = vi.fn();
    render(<StateGraphEditor {...initialProps} onArtifactsChange={onArtifactsChange} />);

    const panel = screen.getByRole('complementary', { name: 'Artifacts' });
    expect(panel).toHaveTextContent('Report workflow');
    expect(panel).toHaveTextContent('Result page');
    expect(artifactsButton()).toHaveAttribute('aria-expanded', 'true');
    expect(onArtifactsChange).not.toHaveBeenCalled();

    fireEvent.click(artifactsButton());
    expect(screen.queryByRole('complementary', { name: 'Artifacts' })).not.toBeInTheDocument();
    expect(artifactsButton()).toHaveAttribute('aria-expanded', 'false');
    expect(onArtifactsChange).toHaveBeenLastCalledWith(false);

    fireEvent.click(artifactsButton());
    expect(artifactsButton()).toHaveAttribute('aria-expanded', 'true');
    expect(onArtifactsChange).toHaveBeenLastCalledWith(true);

    fireEvent.click(screen.getByRole('button', { name: 'Close artifacts' }));
    expect(screen.queryByRole('complementary', { name: 'Artifacts' })).not.toBeInTheDocument();
    expect(artifactsButton()).toHaveAttribute('aria-expanded', 'false');
    expect(onArtifactsChange.mock.calls).toEqual([[false], [true], [false]]);
  });

  it('respects an explicitly collapsed artifact panel', () => {
    const onArtifactsChange = vi.fn();
    render(<StateGraphEditor {...initialProps} defaultShowArtifacts={false} onArtifactsChange={onArtifactsChange} />);

    expect(screen.queryByRole('complementary', { name: 'Artifacts' })).not.toBeInTheDocument();
    expect(artifactsButton()).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(artifactsButton());
    expect(screen.getByRole('complementary', { name: 'Artifacts' })).toBeInTheDocument();
    expect(onArtifactsChange).toHaveBeenCalledWith(true);
  });

  it('locates existing steps without changing or saving the workflow', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<StateGraphEditor {...initialProps} onSave={onSave} />);

    expect(screen.getByText('selfEvolutionRun.sgeFlowContextTitle')).toBeInTheDocument();
    expect(screen.getByText('selfEvolutionRun.sgeFlowContextHint')).toBeInTheDocument();
    const navigation = screen.getByRole('navigation', { name: 'selfEvolutionRun.sgeQuickLocate' });
    expect(within(navigation).getAllByRole('button').map((node) => node.textContent)).toEqual([
      '1 · Gather sources', '2 · Write report',
    ]);

    fireEvent.click(within(navigation).getByRole('button', { name: '2 · Write report' }));
    await flushEditorWork();
    expect(canvas.focusNode).toHaveBeenCalledWith('write');
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps an empty graph honest and does not invent quick navigation steps', async () => {
    const onValidate = vi.fn().mockResolvedValue([
      { code: 'MISSING_STEPS', message: 'Steps are required' },
    ]);
    render(<StateGraphEditor onValidate={onValidate} />);
    await flushEditorWork();

    expect(screen.queryByRole('navigation', { name: 'selfEvolutionRun.sgeQuickLocate' })).not.toBeInTheDocument();
    expect(screen.getByText('selfEvolutionRun.sgeEmptyStateTitle')).toBeInTheDocument();
    expect(onValidate).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('alert')).toHaveTextContent('selfEvolutionRun.validationErrors.MISSING_STEPS');
  });

  it('retains local and authoritative diagnostics with node location and repair context', async () => {
    const authoritativeError = { code: 'MISSING_CAPABILITY', message: 'Select a capability', nodeId: 'write' };
    const onValidate = vi.fn().mockResolvedValue([authoritativeError]);
    const onRepair = vi.fn();
    render(
      <StateGraphEditor
        {...initialProps}
        initialStateYaml={stateYaml.replace('write: [{to: __end__}]', 'write: [{to: missing_step}]')}
        onValidate={onValidate}
        onRepair={onRepair}
      />,
    );
    await flushEditorWork();

    const diagnostics = screen.getByRole('alert');
    expect(diagnostics).toHaveTextContent('selfEvolutionRun.validationErrors.LOCAL_DANGLING_EDGE');
    expect(diagnostics).toHaveTextContent('selfEvolutionRun.validationErrors.MISSING_CAPABILITY');
    expect(onValidate).toHaveBeenCalledTimes(1);
    fireEvent.click(within(diagnostics).getByRole('button', { name: 'selfEvolutionRun.validationErrors.MISSING_CAPABILITY' }));
    await flushEditorWork();
    expect(canvas.focusNode).toHaveBeenCalledWith('write');

    fireEvent.click(screen.getByRole('button', { name: /selfEvolutionRun.sgeAiRepairBtn$/ }));
    await act(async () => {});
    expect(onRepair).toHaveBeenCalledWith('statemachine', expect.arrayContaining([
      expect.objectContaining({ code: 'LOCAL_DANGLING_EDGE', nodeId: 'write' }),
      authoritativeError,
    ]), { node_ids: [] });
  });

  it('flushes a new node before opening integration repair and locks editing while saving', async () => {
    let finish!: () => void;
    const onSave = vi.fn((_payload: import('./index').SavePayload) => new Promise<void>((resolve) => { finish = resolve; }));
    const onRepair = vi.fn();
    render(<StateGraphEditor {...initialProps} onSave={onSave} onRepair={onRepair} />);
    fireEvent.click(button('sgeAddStepBtn'));
    expect(onSave).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave.mock.calls[0][0].stateYaml).toContain('review_2');
    expect(onSave.mock.calls[0][0].workflowYaml).toContain('review_2');
    expect(onRepair).not.toHaveBeenCalled();
    expect(screen.getByRole('region', { name: 'Graph canvas' })).toHaveAttribute('data-readonly', 'true');
    await act(async () => finish());
    expect(onRepair).toHaveBeenCalledWith('statemachine', expect.any(Array), { node_ids: ['review_2'] });
    await flushEditorWork();
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('exposes a save barrier that parses pending YAML and saves the latest node once', async () => {
    const editorRef = createRef<StateGraphEditorHandle>();
    let finishSave!: () => void;
    const onSave = vi.fn(() => new Promise<void>((resolve) => { finishSave = resolve; }));
    render(<StateGraphEditor {...initialProps} editorRef={editorRef} onSave={onSave} />);
    fireEvent.click(button('sgeViewCode'));
    const latest = stateYaml.replace('id: write', 'id: review').replace('to: write', 'to: review').replace('write:', 'review:');
    fireEvent.change(screen.getByRole('textbox', { name: 'Code editor' }), { target: { value: latest } });
    let barrier!: Promise<boolean>;
    await act(async () => { barrier = editorRef.current!.flushPendingChanges(); });
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave.mock.calls[0]).toEqual([expect.objectContaining({
      stateYaml: expect.stringContaining('id: review'), workflowYaml: expect.stringContaining('id: review'),
    })]);
    let completed = false;
    void barrier.then(() => { completed = true; });
    expect(completed).toBe(false);
    await act(async () => { finishSave(); expect(await barrier).toBe(true); });
    await flushEditorWork();
    expect(onSave).toHaveBeenCalledTimes(1);
    await act(async () => { expect(await editorRef.current!.flushPendingChanges()).toBe(true); });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('reuses an in-flight save through the parent action barrier', async () => {
    const editorRef = createRef<StateGraphEditorHandle>();
    let finishSave!: () => void;
    const onSave = vi.fn(() => new Promise<void>((resolve) => { finishSave = resolve; }));
    render(<StateGraphEditor {...initialProps} editorRef={editorRef} onSave={onSave} />);
    fireEvent.click(button('sgeAddStepBtn'));
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    let barrier!: Promise<boolean>;
    await act(async () => { barrier = editorRef.current!.flushPendingChanges(); });
    expect(onSave).toHaveBeenCalledTimes(1);
    await act(async () => { finishSave(); expect(await barrier).toBe(true); });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('flushes a newer edit after an older in-flight autosave', async () => {
    const editorRef = createRef<StateGraphEditorHandle>();
    const saves: (() => void)[] = [];
    const onSave = vi.fn(() => new Promise<void>((resolve) => { saves.push(resolve); }));
    render(<StateGraphEditor {...initialProps} editorRef={editorRef} onSave={onSave} />);
    fireEvent.click(button('sgeAddStepBtn'));
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    fireEvent.click(button('sgeAddStepBtn'));
    let barrier!: Promise<boolean>;
    let completed = false;
    await act(async () => { barrier = editorRef.current!.flushPendingChanges(); void barrier.then(() => { completed = true; }); });
    expect(onSave).toHaveBeenCalledTimes(2);
    expect(onSave.mock.calls[1]).toEqual([expect.objectContaining({ stateYaml: expect.stringContaining('review_3') })]);
    await act(async () => { saves[0](); });
    expect(completed).toBe(false);
    await act(async () => { saves[1](); expect(await barrier).toBe(true); });
    await flushEditorWork();
    expect(onSave).toHaveBeenCalledTimes(2);
  });

  it('blocks the parent action on invalid state YAML and can recover without losing edits', async () => {
    const editorRef = createRef<StateGraphEditorHandle>();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<StateGraphEditor {...initialProps} editorRef={editorRef} onSave={onSave} />);
    fireEvent.click(button('sgeViewCode'));
    fireEvent.change(screen.getByRole('textbox', { name: 'Code editor' }), { target: { value: 'steps: [' } });
    await act(async () => { expect(await editorRef.current!.flushPendingChanges()).toBe(false); });
    await flushEditorWork();
    await act(async () => { expect(await editorRef.current!.flushPendingChanges()).toBe(false); });
    expect(onSave).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole('textbox', { name: 'Code editor' }), { target: { value: stateYaml } });
    await act(async () => { expect(await editorRef.current!.flushPendingChanges()).toBe(true); });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('blocks the parent action on invalid workflow YAML instead of publishing the previous model', async () => {
    const editorRef = createRef<StateGraphEditorHandle>();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<StateGraphEditor {...initialProps} editorRef={editorRef} onSave={onSave} />);
    fireEvent.click(button('sgeTabUi'));
    fireEvent.click(button('sgeViewCode'));
    fireEvent.change(screen.getByRole('textbox', { name: 'Code editor' }), { target: { value: 'steps: [' } });
    await act(async () => { expect(await editorRef.current!.flushPendingChanges()).toBe(false); });
    expect(onSave).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole('textbox', { name: 'Code editor' }), { target: { value: workflowYaml } });
    await act(async () => { expect(await editorRef.current!.flushPendingChanges()).toBe(true); });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('returns a failed save to the parent and retains the node for an explicit retry', async () => {
    const editorRef = createRef<StateGraphEditorHandle>();
    const onSave = vi.fn().mockRejectedValueOnce(new Error('save failed')).mockResolvedValue(undefined);
    render(<StateGraphEditor {...initialProps} editorRef={editorRef} onSave={onSave} />);
    fireEvent.click(button('sgeAddStepBtn'));
    await act(async () => { expect(await editorRef.current!.flushPendingChanges()).toBe(false); });
    expect(screen.getByText('selfEvolutionRun.sgeSaveError')).toBeVisible();
    expect(screen.getByRole('button', { name: '3 · Review report' })).toBeVisible();
    await act(async () => { expect(await editorRef.current!.flushPendingChanges()).toBe(true); });
    expect(onSave).toHaveBeenCalledTimes(2);
  });

  it('keeps the new node editable and does not repair if the latest save fails', async () => {
    const onSave = vi.fn().mockRejectedValue(new Error('conflict'));
    const onRepair = vi.fn();
    render(<StateGraphEditor {...initialProps} onSave={onSave} onRepair={onRepair} />);
    fireEvent.click(button('sgeAddStepBtn'));
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    expect(onRepair).not.toHaveBeenCalled();
    expect(screen.getByText('selfEvolutionRun.sgeSaveError')).toBeVisible();
    expect(screen.getByRole('region', { name: 'Graph canvas' })).toHaveAttribute('data-readonly', 'false');
    expect(screen.getByRole('button', { name: '3 · Review report' })).toBeVisible();
    onSave.mockResolvedValue(undefined);
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    expect(onRepair).toHaveBeenCalledWith('statemachine', expect.any(Array), { node_ids: ['review_2'] });
  });

  it('flushes pending YAML parsing before repair and leaves no delayed save behind', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const onRepair = vi.fn();
    render(<StateGraphEditor {...initialProps} onSave={onSave} onRepair={onRepair} />);
    fireEvent.click(button('sgeViewCode'));
    fireEvent.change(screen.getByRole('textbox', { name: 'Code editor' }), { target: { value: stateYaml.replace('id: write', 'id: review').replace('to: write', 'to: review').replace('write:', 'review:') } });
    fireEvent.click(button('sgeViewPreview'));
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave.mock.calls[0][0].stateYaml).toContain('id: review');
    expect(onSave.mock.calls[0][0].workflowYaml).toContain('id: review');
    expect(onRepair).toHaveBeenCalledWith('statemachine', expect.any(Array), { node_ids: ['review'] });
    await flushEditorWork();
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('blocks repair on invalid pending YAML until the user fixes it', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const onRepair = vi.fn();
    render(<StateGraphEditor {...initialProps} onSave={onSave} onRepair={onRepair} />);
    fireEvent.click(button('sgeViewCode'));
    fireEvent.change(screen.getByRole('textbox', { name: 'Code editor' }), { target: { value: 'steps: [' } });
    fireEvent.click(button('sgeViewPreview'));
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    await flushEditorWork();
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    expect(onSave).not.toHaveBeenCalled();
    expect(onRepair).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent('selfEvolutionRun.validationErrors.V10_YAML_SYNTAX');
    fireEvent.click(button('sgeViewCode'));
    fireEvent.change(screen.getByRole('textbox', { name: 'Code editor' }), { target: { value: stateYaml } });
    fireEvent.click(button('sgeViewPreview'));
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onRepair).toHaveBeenCalledTimes(1);
  });

  it('waits for an already running save without sending a duplicate snapshot', async () => {
    let finish!: () => void;
    const onSave = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
    const onRepair = vi.fn();
    render(<StateGraphEditor {...initialProps} onSave={onSave} onRepair={onRepair} />);
    fireEvent.click(button('sgeAddStepBtn'));
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    expect(onSave).toHaveBeenCalledTimes(1);
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    expect(onRepair).not.toHaveBeenCalled();
    expect(onSave).toHaveBeenCalledTimes(1);
    await act(async () => finish());
    expect(onRepair).toHaveBeenCalledWith('statemachine', expect.any(Array), { node_ids: ['review_2'] });
  });

  it('does not rewrite a clean draft just to open repair', async () => {
    const onSave = vi.fn();
    const onRepair = vi.fn();
    render(<StateGraphEditor {...initialProps} onSave={onSave} onRepair={onRepair} />);
    await act(async () => fireEvent.click(button('sgeAiRepairBtn')));
    expect(onSave).not.toHaveBeenCalled();
    expect(onRepair).toHaveBeenCalledTimes(1);
  });

  it('retries the failed integration scope even when the new node is already connected', async () => {
    const onRepair = vi.fn();
    render(<StateGraphEditor {...initialProps} onRepair={onRepair} repairRetryTarget="node_integration" repairDiagnostics={[{ code: 'E_REPAIR_MODE_CHANGED', nodeId: 'write', message: 'Keep human review' }]} />);
    await act(async () => fireEvent.click(button('sgeRetryRepair')));
    expect(onRepair).toHaveBeenCalledWith('node_integration', expect.any(Array), { node_ids: [] });
  });

  it('locates failed repair diagnostics without applying the rejected candidate', async () => {
    render(<StateGraphEditor {...initialProps} repairDiagnostics={[{ code: 'E_REPAIR_MODE_CHANGED', nodeId: 'write', message: 'Keep human review' }]} />);
    fireEvent.click(screen.getByRole('button', { name: 'selfEvolutionRun.validationErrors.E_REPAIR_MODE_CHANGED' }));
    await flushEditorWork();
    expect(canvas.focusNode).toHaveBeenCalledWith('write');
  });

  it('shows complete generation diagnostics in the log even without a skill analysis', async () => {
    const logs = Array.from({ length: 12 }, (_, i) => `Warning ${i + 1}`).join('\n');
    render(<StateGraphEditor {...initialProps} generationLog={logs} />);
    fireEvent.click(button('sgeViewBrief'));
    expect(screen.getByText(/Warning 1\s+Warning 2/).textContent).toBe(logs);
  });

  it('preserves code, UI, and scenario navigation without saving', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<StateGraphEditor {...initialProps} onSave={onSave} />);

    fireEvent.click(button('sgeViewCode'));
    expect((screen.getByRole('textbox', { name: 'Code editor' }) as HTMLTextAreaElement).value).toContain('gather');
    expect(button('sgeTabUi')).toBeDisabled();
    fireEvent.click(screen.getByText('workflow.yaml'));
    expect((screen.getByRole('textbox', { name: 'Code editor' }) as HTMLTextAreaElement).value).toContain('Report workflow');
    fireEvent.click(button('sgeViewPreview'));
    expect(screen.getByRole('region', { name: 'UI editor' })).toHaveTextContent('Report workflow');

    fireEvent.click(button('sgeTabScenario'));
    expect(screen.getByText('Prepare a source-backed report.')).toBeInTheDocument();
    fireEvent.click(button('sgeViewCode'));
    expect(screen.getByRole('textbox', { name: 'Code editor' })).toHaveAttribute('data-language', 'markdown');
    fireEvent.click(button('sgeViewPreview'));
    expect(screen.getByText('Prepare a source-backed report.')).toBeInTheDocument();
    fireEvent.click(button('sgeTabStatemachine'));
    expect(screen.getByRole('navigation', { name: 'selfEvolutionRun.sgeQuickLocate' })).toBeInTheDocument();
    await flushEditorWork();
    expect(onSave).not.toHaveBeenCalled();
  });

  it('allows readonly browsing and location while blocking edits, saves, and validation', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const onValidate = vi.fn().mockResolvedValue([]);
    render(<StateGraphEditor {...initialProps} readonly onSave={onSave} onValidate={onValidate} onRepair={vi.fn()} />);

    expect(screen.queryByRole('button', { name: /selfEvolutionRun.sgeAddStepBtn$/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /selfEvolutionRun.sgeAiRepairBtn$/ })).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Graph canvas' })).toHaveAttribute('data-readonly', 'true');
    expect(screen.getByRole('complementary', { name: 'Artifacts' })).toHaveAttribute('data-readonly', 'true');
    fireEvent.click(screen.getByRole('button', { name: '1 · Gather sources' }));
    fireEvent.click(artifactsButton());
    expect(artifactsButton()).toHaveAttribute('aria-expanded', 'false');

    fireEvent.keyDown(window, { key: 's', ctrlKey: true });
    fireEvent.keyDown(window, { key: 'z', metaKey: true });
    await flushEditorWork();
    expect(canvas.focusNode).toHaveBeenCalledWith('gather');
    fireEvent.click(button('sgeViewCode'));
    const editor = screen.getByRole('textbox', { name: 'Code editor' });
    expect(editor).toHaveAttribute('readonly');
    fireEvent.change(editor, { target: { value: 'steps: []' } });
    await flushEditorWork();
    expect(onSave).not.toHaveBeenCalled();
    expect(onValidate).not.toHaveBeenCalled();
    expect(canvas.addNode).not.toHaveBeenCalled();
  });
});
