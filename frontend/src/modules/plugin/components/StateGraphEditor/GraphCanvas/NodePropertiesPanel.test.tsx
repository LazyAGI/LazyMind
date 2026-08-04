import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import NodePropertiesPanel from "./NodePropertiesPanel";
import type { GraphModel, StepNode } from "../core/model";
import { VIRTUAL_START, createEmptyModel } from "../core/model";

const listToolAssetsMock = vi.fn();
vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssets: (...args: unknown[]) => listToolAssetsMock(...args),
}));

function makeNode(overrides: Partial<StepNode> = {}): StepNode {
  return {
    id: "write_outline",
    label: "Write outline",
    mode: "auto",
    inputs: [],
    outputs: [],
    transitions: [],
    ...overrides,
  };
}

function makeModel(overrides: Partial<GraphModel> = {}): GraphModel {
  return { ...createEmptyModel(), ...overrides };
}

describe("NodePropertiesPanel", () => {
  beforeEach(() => {
    listToolAssetsMock.mockReset();
    listToolAssetsMock.mockResolvedValue([]);
  });

  // Must run before any other test: NodePropertiesPanel caches the tool list
  // at module scope after the first successful fetch, so later renders would
  // short-circuit and never call listToolAssets again.
  it("loads system tools on mount", async () => {
    listToolAssetsMock.mockResolvedValueOnce([{ id: "search", name: "Search" }]);
    renderWithProviders(
      <NodePropertiesPanel
        node={makeNode()}
        model={makeModel()}
        onClose={vi.fn()}
        onChange={vi.fn(() => true)}
        onDelete={vi.fn()}
      />,
    );
    await waitFor(() => expect(listToolAssetsMock).toHaveBeenCalled());
  });

  it("renders a minimal panel for the virtual start node", () => {
    const startNode = makeNode({ id: VIRTUAL_START, label: "Start", transitions: [] });
    renderWithProviders(
      <NodePropertiesPanel
        node={startNode}
        model={makeModel()}
        onClose={vi.fn()}
        onChange={vi.fn(() => true)}
        onDelete={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.nodePropsStartNodeTitle")).toBeInTheDocument();
    expect(screen.queryByText("selfEvolutionRun.stateGraphDeleteStep")).not.toBeInTheDocument();
  });

  it("renders the full panel with step id and label for a regular node", () => {
    renderWithProviders(
      <NodePropertiesPanel
        node={makeNode()}
        model={makeModel()}
        onClose={vi.fn()}
        onChange={vi.fn(() => true)}
        onDelete={vi.fn()}
      />,
    );
    expect(screen.getByDisplayValue("write_outline")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Write outline")).toBeInTheDocument();
  });

  it("commits the id draft on blur and reverts with a conflict message when rejected", () => {
    const onChange = vi.fn(() => false);
    renderWithProviders(
      <NodePropertiesPanel
        node={makeNode()}
        model={makeModel()}
        onClose={vi.fn()}
        onChange={onChange}
        onDelete={vi.fn()}
      />,
    );
    const idInput = screen.getByDisplayValue("write_outline");
    fireEvent.change(idInput, { target: { value: "duplicate_id" } });
    fireEvent.blur(idInput);

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ id: "duplicate_id" }));
    expect(screen.getByDisplayValue("write_outline")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.stateGraphFieldStepIdConflict")).toBeInTheDocument();
  });

  it("calls onDelete with the node id when the delete button is clicked", () => {
    const onDelete = vi.fn();
    renderWithProviders(
      <NodePropertiesPanel
        node={makeNode()}
        model={makeModel()}
        onClose={vi.fn()}
        onChange={vi.fn(() => true)}
        onDelete={onDelete}
      />,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.stateGraphDeleteStep"));
    expect(onDelete).toHaveBeenCalledWith("write_outline");
  });

  it("hides editing controls in readonly mode", () => {
    renderWithProviders(
      <NodePropertiesPanel
        node={makeNode()}
        model={makeModel()}
        onClose={vi.fn()}
        onChange={vi.fn(() => true)}
        onDelete={vi.fn()}
        readonly
      />,
    );
    expect(screen.queryByText("selfEvolutionRun.stateGraphDeleteStep")).not.toBeInTheDocument();
  });
});
