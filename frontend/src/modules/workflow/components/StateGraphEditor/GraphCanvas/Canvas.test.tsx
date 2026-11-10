import { createRef, type ReactNode } from 'react';
import { act, cleanup, render, screen } from '@testing-library/react';
import type { Connection, Node, ReactFlowProps } from '@xyflow/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Canvas, { type CanvasHandle } from './Canvas';
import { createEmptyModel, type GraphModel, type StepNode } from '../core/model';

const flow = vi.hoisted(() => ({
  nodes: [] as Node[], setViewport: vi.fn(), screenToFlowPosition: vi.fn(),
  onConnect: undefined as ReactFlowProps['onConnect'],
  zoom: 1.5, pendingFrames: new Map<number, FrameRequestCallback>(), nextFrame: 0,
}));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('./NodePropertiesPanel', () => ({
  default: ({ node }: { node: StepNode }) => <div className="node-props-panel">{node.label} properties</div>,
}));
vi.mock('@xyflow/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@xyflow/react')>();
  return {
    ...actual,
    ReactFlow: ({ nodes = [], nodeTypes, onConnect }: ReactFlowProps) => {
      flow.nodes = nodes;
      flow.onConnect = onConnect;
      const Step = nodeTypes!.step;
      return <div className="react-flow">{nodes.filter(node => node.type === 'step').map(node => (
        <Step key={node.id} id={node.id} type="step" data={node.data} selected={node.selected ?? false}
          dragging={false} isConnectable draggable selectable deletable
          positionAbsoluteX={node.position.x} positionAbsoluteY={node.position.y} zIndex={0} />
      ))}</div>;
    },
    Handle: () => null,
    ReactFlowProvider: ({ children }: { children: ReactNode }) => children,
    useStore: (selector: (state: { nodes: Node[]; transform: number[] }) => unknown) => selector({ nodes: flow.nodes, transform: [0, 0, flow.zoom] }),
    useReactFlow: () => ({
      screenToFlowPosition: flow.screenToFlowPosition,
      zoomIn: vi.fn(), zoomOut: vi.fn(), getZoom: () => flow.zoom,
      getNode: (id: string) => {
        const node = flow.nodes.find((item) => item.id === id);
        return node && { ...node, measured: { width: 260, height: 160 } };
      },
      setViewport: flow.setViewport,
    }),
  };
});

const step: StepNode = { id: 'review', label: 'Review', mode: 'auto', inputs: [], outputs: [], transitions: [] };
const model = { ...createEmptyModel(), nodes: [step], layout: { review: { x: 100, y: 60 } } };

beforeEach(() => {
  flow.nodes = [];
  flow.onConnect = undefined;
  flow.pendingFrames.clear();
  flow.setViewport.mockReset().mockResolvedValue(true);
  flow.screenToFlowPosition.mockReset().mockImplementation((point) => point);
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    const id = ++flow.nextFrame; flow.pendingFrames.set(id, callback); return id;
  });
  vi.stubGlobal('cancelAnimationFrame', (id: number) => flow.pendingFrames.delete(id));
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const width = this.classList.contains('react-flow') && this.parentElement?.querySelector('.node-props-panel') ? 624 : 1004;
    return { left: 276, top: 178, width, height: 500, right: 276 + width, bottom: 678, x: 276, y: 178, toJSON() {} };
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function finishFrame() {
  act(() => {
    const frames = [...flow.pendingFrames.values()];
    flow.pendingFrames.clear();
    frames.forEach((callback) => callback(0));
  });
}

describe('canvas authoring geometry', () => {
  it.each([true, false])('keeps material chips renderable after connecting a step (labels: %s)', (withLabels) => {
    const source: StepNode = {
      ...step,
      inputs: [{ material: 'notes', required: true, alternatives: ['backup'] }],
      outputs: [{ material: 'summary', required: true }, { material: 'report', required: false }],
    };
    const graph: GraphModel = {
      ...model,
      nodes: [source, { ...step, id: 'write', label: 'Write' }],
      slots: withLabels ? {
        summary: { id: 'summary', type: 'text', label: 'Summary' },
        report: { id: 'report', type: 'text', label: 'Report' },
      } : {},
    };
    const onModelChange = vi.fn();
    const view = render(<Canvas model={graph} errors={[]} onModelChange={onModelChange} />);
    const connection: Connection = { source: 'review', target: 'write', sourceHandle: null, targetHandle: null };

    act(() => flow.onConnect?.(connection));

    expect(screen.getByText(withLabels ? 'Summary' : 'summary')).toBeVisible();
    expect(screen.getByText(withLabels ? 'Report' : 'report')).toBeVisible();
    const card = flow.nodes.find(node => node.id === 'review')!;
    expect(card.data.inputs).toEqual(['notes', 'backup']);
    expect(card.data.outputs).toEqual(['summary', 'report']);
    expect(card.data.transitions).toEqual([{ to: 'write' }]);
    const updated = onModelChange.mock.calls[0][0];
    expect(updated.nodes[0]).toEqual({ ...source, transitions: [{ to: 'write' }] });

    view.rerender(<Canvas model={updated} errors={[]} onModelChange={onModelChange} />);
    expect(screen.getByText(withLabels ? 'Summary' : 'summary')).toBeVisible();
    act(() => flow.onConnect?.(connection));
    expect(onModelChange).toHaveBeenCalledTimes(1);
  });

  it('blocks the imperative add entry point while readonly and enables it when conversion finishes', () => {
    const ref = createRef<CanvasHandle>();
    const onModelChange = vi.fn();
    const view = render(<Canvas model={model} errors={[]} onModelChange={onModelChange} canvasRef={ref} readonly />);
    act(() => ref.current?.addNode());
    expect(onModelChange).not.toHaveBeenCalled();
    view.rerender(<Canvas model={model} errors={[]} onModelChange={onModelChange} canvasRef={ref} />);
    act(() => ref.current?.addNode());
    const updated = onModelChange.mock.calls[0][0];
    view.rerender(<Canvas model={updated} errors={[]} onModelChange={onModelChange} canvasRef={ref} />);
    expect(screen.getByText('selfEvolutionRun.canvasNewStepLabel properties')).toBeVisible();
  });
  it('centers the measured node in the resized flow after opening its properties', () => {
    const ref = createRef<CanvasHandle>();
    const onModelChange = vi.fn();
    render(<Canvas model={model} errors={[]} onModelChange={onModelChange} canvasRef={ref} />);
    act(() => { expect(ref.current?.focusNode('review')).toBe(true); });
    expect(screen.getByText('Review properties')).toBeInTheDocument();
    expect(flow.setViewport).not.toHaveBeenCalled();
    finishFrame();
    const viewport = flow.setViewport.mock.calls[0][0];
    expect((100 + 260 / 2) * viewport.zoom + viewport.x).toBe(624 / 2);
    expect((60 + 160 / 2) * viewport.zoom + viewport.y).toBe(500 / 2);
    expect(onModelChange).not.toHaveBeenCalled();
  });

  it('adds steps at the visible flow center while the properties panel is open', () => {
    const ref = createRef<CanvasHandle>();
    const onModelChange = vi.fn();
    render(<Canvas model={model} errors={[]} onModelChange={onModelChange} canvasRef={ref} />);
    act(() => { ref.current?.focusNode('review'); });
    finishFrame();
    act(() => { ref.current?.addNode(); });
    expect(flow.screenToFlowPosition).toHaveBeenCalledWith({ x: 276 + 624 / 2, y: 178 + 500 / 2 });
    expect(onModelChange).toHaveBeenCalledTimes(1);
    expect(onModelChange.mock.calls[0][0].layout.review).toEqual(model.layout.review);
  });

  it('lays out generated steps in execution order and relocates obsolete empty-canvas terminals', () => {
    const first = { ...step, id: 'first', transitions: [{ to: 'second' }] };
    const second = { ...step, id: 'second', transitions: [{ to: '__end__' }] };
    render(<Canvas model={{ ...model, nodes: [second, first], startTransitions: [{ to: 'first' }], layout: {
      __start__: { x: 80, y: 200 }, __end__: { x: 440, y: 200 },
    } }} errors={[]} onModelChange={vi.fn()} />);
    const nodes = ['__start__', 'first', 'second', '__end__'].map(id => flow.nodes.find(node => node.id === id)!);
    for (let i = 1; i < nodes.length; i++) {
      expect(nodes[i].position.x).toBeGreaterThan(nodes[i - 1].position.x + (nodes[i - 1].width || 80));
    }
  });

  it('preserves manually positioned terminals in an empty draft', () => {
    const layout = { __start__: { x: 50, y: 90 }, __end__: { x: 500, y: 150 } };
    render(<Canvas model={{ ...createEmptyModel(), layout }} errors={[]} onModelChange={vi.fn()} />);
    expect(flow.nodes.find(node => node.id === '__start__')?.position).toEqual(layout.__start__);
    expect(flow.nodes.find(node => node.id === '__end__')?.position).toEqual(layout.__end__);
  });

  it('separates cards without saved positions and preserves explicit node widths', () => {
    const second = { ...step, id: 'write', label: 'Write' };
    render(<Canvas model={{ ...model, nodes: [step, second], layout: { review: { x: 25, y: 40, width: 315 } } }} errors={[]} onModelChange={vi.fn()} />);
    const firstCard = flow.nodes.find((node) => node.id === 'review')!;
    expect(firstCard.position).toMatchObject({ x: 25, y: 40 });
    expect(firstCard.width).toBe(315);
    cleanup();
    render(<Canvas model={{ ...model, nodes: [step, second], layout: {} }} errors={[]} onModelChange={vi.fn()} />);
    const cards = flow.nodes.filter((node) => node.type === 'step');
    expect(cards[1].position.x).toBeGreaterThan(cards[0].position.x + cards[0].width!);
  });
});
