import { fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import ArtifactPanel from "./index";
import type { GraphModel } from "../core/model";

function emptyModel(): GraphModel {
  return {
    nodes: [],
    slots: {},
    layout: {},
    edgeLayout: {},
    startTransitions: [],
  } as unknown as GraphModel;
}

describe("ArtifactPanel", () => {
  it("shows the empty state when there are no artifacts", () => {
    renderWithProviders(
      <ArtifactPanel model={emptyModel()} onClose={vi.fn()} onModelChange={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.artifactPanelEmpty")).toBeInTheDocument();
  });

  it("lists existing artifacts by their display name and type", () => {
    const model = emptyModel();
    model.slots = {
      summary: { id: "summary", type: "text", label: "Summary" },
    };
    renderWithProviders(
      <ArtifactPanel model={model} onClose={vi.fn()} onModelChange={vi.fn()} />,
    );
    expect(screen.getByText("Summary")).toBeInTheDocument();
    expect(screen.getByText("(summary)")).toBeInTheDocument();
  });

  it("rejects an invalid new artifact id and does not call onModelChange", () => {
    const onModelChange = vi.fn();
    renderWithProviders(
      <ArtifactPanel model={emptyModel()} onClose={vi.fn()} onModelChange={onModelChange} />,
    );

    fireEvent.click(screen.getByRole("button", { name: /artifactPanelAdd$/ }));
    fireEvent.change(
      screen.getByPlaceholderText("selfEvolutionRun.artifactPanelFieldIdPlaceholder"),
      { target: { value: "bad id!" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "selfEvolutionRun.artifactPanelConfirmAdd" }));

    expect(
      screen.getByText("selfEvolutionRun.artifactPanelIdErrorInvalid"),
    ).toBeInTheDocument();
    expect(onModelChange).not.toHaveBeenCalled();
  });

  it("adds a new text artifact with a valid id", () => {
    const onModelChange = vi.fn();
    renderWithProviders(
      <ArtifactPanel model={emptyModel()} onClose={vi.fn()} onModelChange={onModelChange} />,
    );

    fireEvent.click(screen.getByRole("button", { name: /artifactPanelAdd$/ }));
    fireEvent.change(
      screen.getByPlaceholderText("selfEvolutionRun.artifactPanelFieldIdPlaceholder"),
      { target: { value: "notes" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "selfEvolutionRun.artifactPanelConfirmAdd" }));

    expect(onModelChange).toHaveBeenCalledTimes(1);
    const updater = onModelChange.mock.calls[0][0];
    const next = updater(emptyModel());
    expect(next.slots.notes).toMatchObject({ id: "notes", type: "text" });
  });

  it("deletes an artifact and scrubs references to it from nodes and transitions", async () => {
    const onModelChange = vi.fn();
    const model = emptyModel();
    model.slots = { notes: { id: "notes", type: "text" } };
    model.nodes = [
      {
        id: "step1",
        label: "Step 1",
        mode: "auto",
        inputs: [{ material: "notes", required: true }],
        outputs: [{ material: "notes" }],
        transitions: [],
      },
    ] as unknown as GraphModel["nodes"];

    renderWithProviders(
      <ArtifactPanel model={model} onClose={vi.fn()} onModelChange={onModelChange} />,
    );

    fireEvent.click(screen.getByRole("button", { name: "selfEvolutionRun.artifactPanelDeleteTooltip" }));
    fireEvent.click(await screen.findByRole("button", { name: "selfEvolutionRun.artifactPanelDeleteOk" }));

    await waitFor(() => expect(onModelChange).toHaveBeenCalledTimes(1));
    const updater = onModelChange.mock.calls[0][0];
    const next = updater(model);
    expect(next.slots.notes).toBeUndefined();
    expect(next.nodes[0].inputs).toHaveLength(0);
    expect(next.nodes[0].outputs).toHaveLength(0);
  });

  it("closes the panel via the close button when not inline", () => {
    const onClose = vi.fn();
    renderWithProviders(
      <ArtifactPanel model={emptyModel()} onClose={onClose} onModelChange={vi.fn()} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "selfEvolutionRun.artifactPanelClose" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("does not render the close button when inline", () => {
    renderWithProviders(
      <ArtifactPanel model={emptyModel()} onClose={vi.fn()} onModelChange={vi.fn()} inline />,
    );
    expect(
      screen.queryByRole("button", { name: "selfEvolutionRun.artifactPanelClose" }),
    ).not.toBeInTheDocument();
  });

  it("hides edit/delete/add controls in readonly mode", () => {
    const model = emptyModel();
    model.slots = { notes: { id: "notes", type: "text" } };
    renderWithProviders(
      <ArtifactPanel model={model} onClose={vi.fn()} onModelChange={vi.fn()} readonly />,
    );
    expect(screen.queryByRole("button", { name: "selfEvolutionRun.artifactPanelEdit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "selfEvolutionRun.artifactPanelAdd" })).not.toBeInTheDocument();
  });
});
