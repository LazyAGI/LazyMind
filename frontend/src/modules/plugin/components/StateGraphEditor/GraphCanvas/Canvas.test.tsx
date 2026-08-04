import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import Canvas, { type CanvasHandle } from "./Canvas";
import { VIRTUAL_END, VIRTUAL_START, createEmptyModel } from "../core/model";
import type { GraphModel } from "../core/model";

vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssets: vi.fn().mockResolvedValue([]),
}));

function makeModel(overrides: Partial<GraphModel> = {}): GraphModel {
  return {
    ...createEmptyModel(),
    nodes: [
      { id: "write_outline", label: "Write outline", mode: "auto", inputs: [], outputs: [], transitions: [] },
    ],
    startTransitions: [{ to: "write_outline" }],
    layout: {
      [VIRTUAL_START]: { x: 0, y: 0 },
      write_outline: { x: 200, y: 0 },
      [VIRTUAL_END]: { x: 400, y: 0 },
    },
    ...overrides,
  };
}

describe("Canvas", () => {
  beforeEach(() => {
    // ReactFlow measures nodes via ResizeObserver, which jsdom doesn't implement;
    // the global stub from setup.ts covers construction but never fires callbacks.
  });

  it("renders the start node, step node and end node from the model", async () => {
    renderWithProviders(
      <Canvas model={makeModel()} errors={[]} onModelChange={vi.fn()} />,
    );
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.stepNodeStart")).toBeInTheDocument();
    });
    expect(screen.getByText("selfEvolutionRun.stepNodeEnd")).toBeInTheDocument();
    expect(screen.getByText("write_outline")).toBeInTheDocument();
  });

  it("opens the properties panel when a step node is clicked", async () => {
    const { container } = renderWithProviders(
      <Canvas model={makeModel()} errors={[]} onModelChange={vi.fn()} />,
    );
    await waitFor(() => expect(screen.getByText("write_outline")).toBeInTheDocument());
    const stepNodeEl = container.querySelector('[data-id="write_outline"]')!;
    fireEvent.click(stepNodeEl);
    await waitFor(() => {
      expect(container.querySelector(".node-props-panel")).toBeInTheDocument();
    });
  });

  it("adds a new step node when addNode is invoked through the imperative handle", async () => {
    const handleRef: { current: CanvasHandle | null } = { current: null };
    const onModelChange = vi.fn();
    renderWithProviders(
      <Canvas
        model={makeModel()}
        errors={[]}
        onModelChange={onModelChange}
        canvasRef={(instance) => { handleRef.current = instance; }}
      />,
    );
    await waitFor(() => expect(handleRef.current).not.toBeNull());
    act(() => handleRef.current!.addNode());
    await waitFor(() => expect(onModelChange).toHaveBeenCalled());
    const nextModel = onModelChange.mock.calls[0][0] as GraphModel;
    expect(nextModel.nodes.length).toBe(2);
  });

  it("marks a node with hasError styling when validation errors reference it", async () => {
    const { container } = renderWithProviders(
      <Canvas
        model={makeModel()}
        errors={[{ code: "missing_input", message: "missing", nodeId: "write_outline" }]}
        onModelChange={vi.fn()}
      />,
    );
    await waitFor(() => {
      expect(container.querySelector(".step-node.has-error")).toBeInTheDocument();
    });
  });
});
