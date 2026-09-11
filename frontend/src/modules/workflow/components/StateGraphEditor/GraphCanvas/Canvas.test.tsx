import { createRef, type ReactNode } from 'react';
import { act, cleanup, render, screen } from '@testing-library/react';
import type { Node } from '@xyflow/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Canvas, { type CanvasHandle } from './Canvas';
import { createEmptyModel, type StepNode } from '../core/model';

const flow = vi.hoisted(() => ({
  nodes: [] as Node[], setViewport: vi.fn(), screenToFlowPosition: vi.fn(),
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
    ReactFlow: ({ nodes }: { nodes: Node[] }) => { flow.nodes = nodes; return <div className="react-flow" />; },
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
  flow.pendingFrames.clear();
  flow.setViewport.mockReset().mockResolvedValue(true);
  flow.screenToFlowPosition.mockReset().mockImplementation((point) => point);
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
