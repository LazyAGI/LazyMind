import { useState } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import NodePropertiesPanel from './NodePropertiesPanel';
import { createEmptyModel, VIRTUAL_END, type StepNode } from '../core/model';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string, values?: { number?: number }) => values?.number ? `${key} ${values.number}` : key }),
}));
vi.mock('@/modules/memory/toolApi', () => ({ listToolAssets: vi.fn().mockResolvedValue([]) }));

const step: StepNode = {
  id: 'summarize', label: 'Summarize notes', mode: 'human',
  inputs: [{ material: 'notes', required: true, alternatives: ['backup'] }],
  outputs: [{ material: 'summary' }], transitions: [{ to: VIRTUAL_END }],
  prompt: 'Summarize {{notes}}.', tools: ['search'], capabilities: ['network'],
};
const model = {
  ...createEmptyModel(), nodes: [step], startTransitions: [{ to: step.id }],
  slots: {
    notes: { id: 'notes', type: 'text', label: 'Notes', external: true },
    backup: { id: 'backup', type: 'text', label: 'Backup' },
    summary: { id: 'summary', type: 'text', label: 'Summary' },
  },
};
const key = (name: string) => `selfEvolutionRun.${name}`;

function Editor({ onChange = vi.fn(() => true), readonly = false }: { onChange?: (node: StepNode) => boolean; readonly?: boolean }) {
  const [node, setNode] = useState(step);
  return (
    <NodePropertiesPanel
      node={node} model={{ ...model, nodes: [node] }} readonly={readonly}
      onClose={vi.fn()} onDelete={vi.fn()}
      onChange={(next) => { const accepted = onChange(next); if (accepted) setNode(next); return accepted; }}
      visualContent={<div>Appearance controls</div>}
    />
  );
}

afterEach(cleanup);

describe('step authoring panel', () => {
  it('keeps edits and input fallback semantics when moving between task, materials and flow tabs', async () => {
    const onChange = vi.fn(() => true);
    render(<Editor onChange={onChange} />);
    await act(async () => {});
    expect(screen.getByText(key('sgeEditingStep') + ' 1')).toBeInTheDocument();
    expect(screen.getByText(key('stepNodeStart'))).toBeInTheDocument();
    expect(screen.getByText(key('stepNodeEnd'))).toBeInTheDocument();
    fireEvent.change(screen.getByRole('textbox', { name: key('stateGraphFieldLabel') }), { target: { value: 'Review notes' } });
    expect(onChange).toHaveBeenLastCalledWith({ ...step, label: 'Review notes' });
    fireEvent.click(screen.getByRole('tab', { name: key('sgeStepTabMaterials') }));
    expect(screen.queryByRole('textbox', { name: key('stateGraphFieldLabel') })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: key('stateGraphSlotRequired') }));
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      label: 'Review notes', inputs: [{ material: 'notes', required: false, alternatives: undefined }],
      prompt: step.prompt, tools: ['search'], capabilities: ['network'],
    }));
    fireEvent.click(screen.getByRole('tab', { name: key('sgeStepTabFlow') }));
    fireEvent.click(screen.getByText(key('stateGraphAddBranch')));
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ transitions: [{ to: VIRTUAL_END }, { to: '' }] }));
    fireEvent.click(screen.getByRole('tab', { name: key('sgeStepTabStep') }));
    expect(screen.getByRole('textbox', { name: key('stateGraphFieldLabel') })).toHaveValue('Review notes');
  });

  it('supports keyboard navigation and keeps the active tab linked to its panel', async () => {
    render(<Editor />);
    await act(async () => {});
    const taskTab = screen.getByRole('tab', { name: key('sgeStepTabStep') });
    taskTab.focus();
    fireEvent.keyDown(taskTab, { key: 'ArrowRight' });
    const materials = screen.getByRole('tab', { name: key('sgeStepTabMaterials') });
    expect(materials).toHaveFocus();
    expect(materials).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tabpanel')).toHaveAttribute('aria-labelledby', materials.id);
    fireEvent.keyDown(materials, { key: 'End' });
    expect(screen.getByText('Appearance controls')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: key('sgeStepTabVisual') })).toHaveFocus();
    fireEvent.keyDown(screen.getByRole('tab', { name: key('sgeStepTabVisual') }), { key: 'Home' });
    expect(taskTab).toHaveFocus();
  });

  it('keeps identifier rejection and feedback in the advanced disclosure', async () => {
    const onChange = vi.fn(() => false);
    render(<Editor onChange={onChange} />);
    await act(async () => {});
    fireEvent.click(screen.getByText(key('sgeAdvancedStepId')));
    const id = screen.getByRole('textbox', { name: key('stateGraphFieldStepId') });
    fireEvent.focus(id);
    fireEvent.change(id, { target: { value: 'duplicate' } });
    fireEvent.keyDown(id, { key: 'Enter' });
    expect(onChange).toHaveBeenCalledWith({ ...step, id: 'duplicate' });
    await waitFor(() => expect(id).toHaveValue('summarize'));
    expect(screen.getByText(key('stateGraphFieldStepIdConflict'))).toBeVisible();
  });

  it('allows read-only inspection without emitting edits from any group', async () => {
    const onChange = vi.fn(() => true);
    render(<Editor readonly onChange={onChange} />);
    await act(async () => {});
    expect(screen.getByRole('textbox', { name: key('stateGraphFieldLabel') })).toHaveAttribute('readonly');
    fireEvent.click(screen.getByRole('tab', { name: key('sgeStepTabMaterials') }));
    expect(screen.getByRole('button', { name: key('stateGraphSlotRequired') })).toBeDisabled();
    screen.getAllByRole('combobox').forEach((input) => expect(input).toBeDisabled());
    fireEvent.click(screen.getByRole('tab', { name: key('sgeStepTabFlow') }));
    expect(screen.getByRole('checkbox')).toBeDisabled();
    screen.getAllByRole('combobox').forEach((input) => expect(input).toBeDisabled());
    expect(screen.getByText(key('stateGraphAddBranch')).closest('button')).toBeDisabled();
    fireEvent.click(screen.getByRole('checkbox'));
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('tab', { name: key('sgeStepTabVisual') }));
    expect(screen.getByText('Appearance controls')).toBeInTheDocument();
  });
});
