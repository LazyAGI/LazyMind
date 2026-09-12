import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ArtifactPanel from './index';
import type { GraphModel, StepNode } from '../core/model';
import type { WorkflowModel } from '../core/workflowModel';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, string>) => {
      const name = key.replace('selfEvolutionRun.', '');
      if (name === 'sgeMaterialNameSeparator') return '、';
      if (name === 'sgeMaterialPage') return `Page: ${options?.name}`;
      if (name === 'artifactJoinedLabel') return `Joined: ${options?.location}`;
      return name;
    },
  }),
}));

afterEach(cleanup);

function graph(patch: Partial<GraphModel> = {}): GraphModel {
  return { nodes: [], slots: {}, layout: {}, edgeLayout: {}, startTransitions: [], ...patch };
}

function step(id: string, label: string, patch: Partial<StepNode> = {}): StepNode {
  return { id, label, mode: 'auto', inputs: [], outputs: [], transitions: [], ...patch };
}

function workflow(): WorkflowModel {
  return {
    id: 'test_workflow', name: 'Test workflow', steps: [], slots: [],
    ui: { tabs: [{ id: 'results_tab', label: 'Results', slots: [{ id: 'report' }] }] },
  };
}

function choose(label: string, option: string) {
  fireEvent.mouseDown(screen.getByLabelText(label));
  fireEvent.click(screen.getByText(option));
}

describe('ArtifactPanel authoring', () => {
  it('creates an automatic legal ID and preserves the latest model when that ID already exists', () => {
    const onModelChange = vi.fn();
    render(<ArtifactPanel model={graph()} onClose={vi.fn()} onModelChange={onModelChange} startAddingToken={1} />);

    expect(screen.getByLabelText('sgeMaterialName')).toHaveFocus();
    fireEvent.change(screen.getByLabelText('sgeMaterialName'), { target: { value: 'Input text' } });
    choose('sgeMaterialSource', 'sgeMaterialSourceUser');
    choose('sgeMaterialQuantity', 'sgeMaterialList');
    fireEvent.click(screen.getByLabelText('sgeMaterialOrdered'));
    fireEvent.click(screen.getByRole('button', { name: 'artifactPanelConfirmAdd' }));

    expect(onModelChange).toHaveBeenCalledTimes(1);
    const update = onModelChange.mock.calls[0][0] as (prev: GraphModel) => GraphModel;
    const first = update(graph());
    const slot = Object.values(first.slots)[0];
    expect(slot.id).toMatch(/^material_[a-zA-Z0-9_]+$/);
    expect(slot).toMatchObject({ label: 'Input text', type: 'text', external: true, cardinality: 'list', ordered: true, allow_manual_add: true });

    const latest = graph({ slots: first.slots, layout: { start: { x: 4, y: 8, width: 333 } } });
    const second = update(latest);
    expect(Object.keys(second.slots)).toHaveLength(2);
    expect(second.slots[slot.id]).toBe(slot);
    expect(Object.values(second.slots).map((item) => item.id)).toEqual(Object.keys(second.slots));
    expect(second.layout).toBe(latest.layout);
  });

  it('keeps custom ID validation, exposes errors in advanced settings, and never overwrites an existing slot', () => {
    const existing = { id: 'report', label: 'Report', type: 'text' };
    const onModelChange = vi.fn();
    render(<ArtifactPanel model={graph({ slots: { report: existing } })} onClose={vi.fn()} onModelChange={onModelChange} startAddingToken={1} />);
    const idInput = screen.getByLabelText('sgeMaterialInternalId');
    const details = idInput.closest('details');
    expect(details).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('sgeMaterialAdvanced'));
    fireEvent.change(idInput, { target: { value: 'bad-id' } });
    fireEvent.click(screen.getByRole('button', { name: 'artifactPanelConfirmAdd' }));
    expect(screen.getByRole('alert')).toHaveTextContent('artifactPanelIdErrorInvalid');
    expect(details).toHaveAttribute('open');
    expect(idInput).toHaveAttribute('aria-invalid', 'true');

    fireEvent.change(idInput, { target: { value: 'report' } });
    fireEvent.click(screen.getByRole('button', { name: 'artifactPanelConfirmAdd' }));
    expect(screen.getByRole('alert')).toHaveTextContent('artifactPanelIdErrorDuplicate');
    expect(onModelChange).not.toHaveBeenCalled();

    fireEvent.change(idInput, { target: { value: 'custom_report' } });
    fireEvent.click(screen.getByRole('button', { name: 'artifactPanelConfirmAdd' }));
    const update = onModelChange.mock.calls[0][0] as (prev: GraphModel) => GraphModel;
    expect(update(graph()).slots.custom_report.id).toBe('custom_report');
    const latest = graph({ slots: { custom_report: { ...existing, id: 'custom_report' } } });
    expect(update(latest)).toBe(latest);
  });

  it('edits by name without changing the internal ID, references, layout, or list options', () => {
    const model = graph({
      slots: { report: { id: 'report', label: 'Report', type: 'text', cardinality: 'list', ordered: true, summary_max_chars: 240 } },
      nodes: [step('review', 'Review', { inputs: [{ material: 'primary', required: true, alternatives: ['report'] }] })],
    });
    const onModelChange = vi.fn();
    render(<ArtifactPanel model={model} onClose={vi.fn()} onModelChange={onModelChange} />);
    expect(screen.queryByText('report')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'artifactPanelEdit' }));
    expect(screen.getByLabelText('sgeMaterialInternalId')).toHaveAttribute('readonly');
    expect(screen.getByLabelText('sgeMaterialInternalId')).toHaveValue('report');
    expect(screen.getByLabelText('sgeMaterialManualAdd')).toBeChecked();
    fireEvent.change(screen.getByLabelText('sgeMaterialName'), { target: { value: 'Final report' } });
    fireEvent.click(screen.getByRole('button', { name: 'artifactPanelSave' }));

    const latest = { ...model, layout: { review: { x: 22, y: 41, width: 360 } } };
    const updated = onModelChange.mock.calls[0][0](latest) as GraphModel;
    expect(updated.slots.report).toMatchObject({ id: 'report', label: 'Final report', cardinality: 'list', ordered: true, allow_manual_add: true, summary_max_chars: 240 });
    expect(updated.nodes).toBe(latest.nodes);
    expect(updated.layout).toBe(latest.layout);
  });

  it('groups sources and includes every producer and consumer, alternatives, page association, and unused hints', () => {
    const model = graph({
      slots: {
        report: { id: 'report', label: 'Report', type: 'text' },
        request: { id: 'request', label: 'Request', type: 'text', external: true },
        unused: { id: 'unused', label: 'Unused', type: 'image' },
        intermediate: { id: 'intermediate', label: 'Intermediate', type: 'text' },
      },
      nodes: [
        step('a', 'Maker A', { outputs: [{ material: 'intermediate' }] }),
        step('b', 'Maker B', { outputs: [{ material: 'intermediate' }] }),
        step('c', 'Consumer A', { inputs: [{ material: 'intermediate', required: true }, { material: 'request', required: true }] }),
        step('d', 'Consumer B', { inputs: [{ material: 'fallback', alternatives: ['intermediate'], required: true }] }),
      ],
    });
    render(<ArtifactPanel model={model} onClose={vi.fn()} onModelChange={vi.fn()} workflowModel={workflow()} />);
    const external = screen.getByRole('region', { name: 'sgeMaterialExternalGroup' });
    const generated = screen.getByRole('region', { name: 'sgeMaterialGeneratedGroup' });
    expect(within(external).getByText('Request')).toBeInTheDocument();
    expect(within(external).getByText('sgeMaterialUserInput')).toBeInTheDocument();
    expect(within(generated).queryByText('Request')).not.toBeInTheDocument();
    expect(within(generated).getByText('Maker A、Maker B')).toBeInTheDocument();
    expect(within(generated).getByText('Consumer A、Consumer B')).toBeInTheDocument();
    expect(within(generated).getByText('Page: Results')).toBeInTheDocument();
    expect(within(generated).getByText('sgeMaterialUnused')).toBeInTheDocument();
    expect(within(generated).getAllByText('sgeMaterialNoProducer')).toHaveLength(2);
    expect(screen.queryByText('intermediate')).not.toBeInTheDocument();
  });

  it('allows page navigation in readonly UI mode while disabling mutations and dragging', () => {
    const onModelChange = vi.fn();
    const onUiModelChange = vi.fn();
    const onTabNavigate = vi.fn();
    const { container, rerender } = render(<ArtifactPanel
      model={graph({ slots: { report: { id: 'report', label: 'Report', type: 'text' }, unused: { id: 'unused', label: 'Unused', type: 'text' } } })}
      onClose={vi.fn()} onModelChange={onModelChange} workflowModel={workflow()} onUiModelChange={onUiModelChange}
      onTabNavigate={onTabNavigate} uiMode readonly startAddingToken={1}
    />);
    expect(screen.queryByRole('button', { name: 'artifactPanelEdit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'artifactPanelAdd' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'artifactJoinMenuLabel' })).not.toBeInTheDocument();
    expect(screen.queryByTitle('artifactRemoveTooltip')).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(container.querySelector('[draggable="true"]')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: /Joined: Results/ }));
    expect(onTabNavigate).toHaveBeenCalledWith('results_tab');
    expect(onModelChange).not.toHaveBeenCalled();
    expect(onUiModelChange).not.toHaveBeenCalled();

    rerender(<ArtifactPanel model={graph()} onClose={vi.fn()} onModelChange={onModelChange} startAddingToken={1} />);
    expect(screen.getByLabelText('sgeMaterialName')).toBeInTheDocument();
    rerender(<ArtifactPanel model={graph()} onClose={vi.fn()} onModelChange={onModelChange} startAddingToken={1} readonly />);
    expect(screen.queryByLabelText('sgeMaterialName')).not.toBeInTheDocument();
  });

  it('keeps UI page assignment connected to the existing slot ID and selected widget', async () => {
    const onUiModelChange = vi.fn();
    const workflowModel = workflow();
    workflowModel.ui!.tabs[0].slots = [];
    render(<ArtifactPanel
      model={graph({ slots: { report: { id: 'report', label: 'Report', type: 'text' } } })}
      onClose={vi.fn()} onModelChange={vi.fn()} uiMode workflowModel={workflowModel} onUiModelChange={onUiModelChange}
    />);
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /artifactJoinMenuLabel/ })); });
    await act(async () => { fireEvent.click(screen.getByRole('menuitem', { name: 'Results' })); });
    expect(onUiModelChange).toHaveBeenCalledWith({
      tabs: [{ id: 'results_tab', label: 'Results', slots: [{ id: 'report' }] }],
      slots: { report: { widgetType: 'text-single' } },
    });
  });

  it('retains deletion cleanup for inputs, alternative inputs, outputs, and material conditions', () => {
    const model = graph({
      slots: { report: { id: 'report', label: 'Report', type: 'text' } },
      nodes: [step('review', 'Review', {
        inputs: [{ material: 'report', required: true }, { material: 'other', alternatives: ['report', 'backup'], required: true }],
        outputs: [{ material: 'report' }],
        skipIf: { material: 'report' },
        transitions: [{ to: '__end__', condition: { material: 'report' } }],
      })],
      startTransitions: [{ to: 'review', condition: { material: 'report' } }],
    });
    const onModelChange = vi.fn();
    render(<ArtifactPanel model={model} onClose={vi.fn()} onModelChange={onModelChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'artifactPanelDeleteTooltip' }));
    fireEvent.click(screen.getByRole('button', { name: 'artifactPanelDeleteOk' }));
    const updated = onModelChange.mock.calls[0][0](model) as GraphModel;
    expect(updated.slots).toEqual({});
    expect(updated.nodes[0].inputs).toEqual([{ material: 'other', alternatives: ['backup'], required: true }]);
    expect(updated.nodes[0].outputs).toEqual([]);
    expect(updated.nodes[0].skipIf).toBeUndefined();
    expect(updated.nodes[0].transitions[0].condition).toBeUndefined();
    expect(updated.startTransitions[0].condition).toBeUndefined();
  });
});
