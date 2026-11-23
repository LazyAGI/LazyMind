import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ReactFlowProvider, type NodeProps } from '@xyflow/react';
import { StepNodeRenderer, type StepNodeData } from './StepNode';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string, values?: { inputs?: string }) => values?.inputs ? `${key}: ${values.inputs}` : key }),
}));

const data: StepNodeData = {
  id: 'review', label: 'Review notes', mode: 'human', inputs: ['notes', 'backup'],
  inputLabels: ['Notes', 'Backup'], outputs: ['summary'], outputLabels: { summary: 'Summary' },
  transitions: [], predecessorIds: [], hasError: false, errorMessages: [], nodeWidth: 240,
  visual: { x: 0, y: 0 }, onResizeEnd: vi.fn(), onResizeDrag: () => ({ width: 240 }), getZoom: () => 1,
};
const props: NodeProps = {
  id: 'review', type: 'step', data, selected: false, dragging: false, isConnectable: true,
  positionAbsoluteX: 0, positionAbsoluteY: 0, zIndex: 0, draggable: true, selectable: true, deletable: true,
};

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('step card authoring details', () => {
  it('uses the editable name in the header while keeping a generated ID out of the card', () => {
    const generated = { ...data, id: 'step_dfb9f0', label: '生成辅助文档' };
    const view = render(<ReactFlowProvider><StepNodeRenderer {...props} id={generated.id} data={generated} /></ReactFlowProvider>);
    expect(screen.getByText('生成辅助文档').closest('.step-node-header')).not.toBeNull();
    expect(screen.queryByText(generated.id)).not.toBeInTheDocument();

    view.rerender(<ReactFlowProvider><StepNodeRenderer {...props} id={generated.id} data={{ ...generated, label: '整理辅助文档' }} /></ReactFlowProvider>);
    expect(screen.getByText('整理辅助文档').closest('.step-node-header')).not.toBeNull();
    expect(screen.queryByText('生成辅助文档')).not.toBeInTheDocument();
    expect(screen.queryByText(generated.id)).not.toBeInTheDocument();
  });

  it('shows the input labels and confirmation mode alongside output labels', () => {
    render(<ReactFlowProvider><StepNodeRenderer {...props} /></ReactFlowProvider>);
    expect(screen.getByText('selfEvolutionRun.sgeNodeInputs: Notes、Backup')).toBeInTheDocument();
    expect(screen.getByText('selfEvolutionRun.sgeNodeUserConfirm')).toBeInTheDocument();
    expect(screen.getByText('Review notes').closest('.step-node-header')).toBeInTheDocument();
    expect(screen.queryByText('review')).not.toBeInTheDocument();
    expect(screen.getByText('Summary')).toBeInTheDocument();
  });

  it('preserves explicit appearance values and hidden output details', () => {
    const customData = {
      ...data, visual: {
        x: 0, y: 0, visible: { outputs: false },
        fill: { type: 'solid' as const, color: '#ff0000' },
        border: { width: 4, radius: 20, color: '#0000ff', style: 'dashed' as const },
      },
    };
    render(<ReactFlowProvider><StepNodeRenderer {...props} data={customData} /></ReactFlowProvider>);
    expect(screen.queryByText('selfEvolutionRun.sgeNodeInputs: Notes、Backup')).not.toBeInTheDocument();
    expect(screen.queryByText('Summary')).not.toBeInTheDocument();
    expect(screen.getByText('Review notes').closest('.step-node')).toHaveStyle({
      background: '#ff0000', borderWidth: '4px', borderRadius: '20px', borderColor: '#0000ff', borderStyle: 'dashed',
    });
  });
});
