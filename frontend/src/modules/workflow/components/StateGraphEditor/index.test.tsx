import { useImperativeHandle } from 'react';
import type { ComponentProps } from 'react';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type GraphCanvas from './GraphCanvas';
import type ArtifactPanel from './ArtifactPanel';
import type YamlEditor from './YamlEditor';
import type UiEditorPanel from './UiEditorPanel';
import StateGraphEditor from './index';

const canvas = vi.hoisted(() => ({
  addNode: vi.fn(),
  focusNode: vi.fn(() => true),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('@/utils/developerMode', () => ({ isDeveloperModeActive: () => false }));

vi.mock('./GraphCanvas', () => ({
  default: function Canvas({ canvasRef, readonly }: ComponentProps<typeof GraphCanvas>) {
    useImperativeHandle(canvasRef, () => canvas);
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

const button = (key: string) => screen.getByRole('button', { name: `selfEvolutionRun.${key}` });
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
    expect(onRepair).toHaveBeenCalledWith('statemachine', expect.arrayContaining([
      expect.objectContaining({ code: 'LOCAL_DANGLING_EDGE', nodeId: 'write' }),
      authoritativeError,
    ]));
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
