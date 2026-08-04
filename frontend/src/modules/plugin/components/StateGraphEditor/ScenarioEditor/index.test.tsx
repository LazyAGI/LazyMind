import { fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import ScenarioEditor, { parseScenario, serializeScenario } from "./index";
import type { StepNode } from "../core/model";

function makeStep(id: string, label: string): StepNode {
  return { id, label, mode: "auto", inputs: [], outputs: [], transitions: [] };
}

describe("ScenarioEditor component", () => {
  it("shows the empty hint when there are no steps", () => {
    renderWithProviders(
      <ScenarioEditor
        steps={[]}
        value={{ overview: "", stepDescriptions: {}, notes: "" }}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.scenarioEditorEmptyHint")).toBeInTheDocument();
  });

  it("renders one row per step with its id and label", () => {
    const steps = [makeStep("write", "Write draft"), makeStep("review", "Review draft")];
    renderWithProviders(
      <ScenarioEditor
        steps={steps}
        value={{ overview: "", stepDescriptions: {}, notes: "" }}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByText("write")).toBeInTheDocument();
    expect(screen.getByText("Write draft")).toBeInTheDocument();
    expect(screen.getByText("review")).toBeInTheDocument();
  });

  it("updates the step description and calls onChange with the merged data", () => {
    const onChange = vi.fn();
    const steps = [makeStep("write", "Write draft")];
    renderWithProviders(
      <ScenarioEditor
        steps={steps}
        value={{ overview: "overview", stepDescriptions: {}, notes: "notes" }}
        onChange={onChange}
      />,
    );

    const input = screen.getByPlaceholderText("selfEvolutionRun.scenarioStepDescPlaceholder");
    fireEvent.change(input, { target: { value: "does the writing" } });

    expect(onChange).toHaveBeenCalledWith({
      overview: "overview",
      notes: "notes",
      stepDescriptions: { write: "does the writing" },
    });
  });
});

describe("serializeScenario", () => {
  it("serializes overview, per-step descriptions, and notes into markdown sections", () => {
    const steps = [makeStep("write", "Write draft")];
    const markdown = serializeScenario(steps, {
      overview: "This plugin drafts text.",
      stepDescriptions: { write: "Writes the initial draft." },
      notes: "Keep it concise.",
    });

    expect(markdown).toContain("## 场景描述");
    expect(markdown).toContain("This plugin drafts text.");
    expect(markdown).toContain("## 工作流程");
    expect(markdown).toContain("### write（Write draft）");
    expect(markdown).toContain("Writes the initial draft.");
    expect(markdown).toContain("## 注意事项");
    expect(markdown).toContain("Keep it concise.");
  });

  it("uses a placeholder for steps without a description", () => {
    const steps = [makeStep("write", "Write draft")];
    const markdown = serializeScenario(steps, { overview: "", stepDescriptions: {}, notes: "" });
    expect(markdown).toContain("（暂无描述）");
  });
});

describe("parseScenario", () => {
  it("round-trips data produced by serializeScenario", () => {
    const steps = [makeStep("write", "Write draft")];
    const original = {
      overview: "This plugin drafts text.",
      stepDescriptions: { write: "Writes the initial draft." },
      notes: "Keep it concise.",
    };
    const markdown = serializeScenario(steps, original);
    const parsed = parseScenario(markdown, steps);

    expect(parsed.overview).toBe(original.overview);
    expect(parsed.stepDescriptions.write).toBe(original.stepDescriptions.write);
    expect(parsed.notes).toBe(original.notes);
  });

  it("returns empty data for an empty markdown string", () => {
    expect(parseScenario("", [])).toEqual({ overview: "", stepDescriptions: {}, notes: "" });
  });

  it("treats unstructured markdown without any recognized section as the overview", () => {
    const parsed = parseScenario("Just some free-form notes.", []);
    expect(parsed.overview).toBe("Just some free-form notes.");
  });

  it("fills in empty descriptions for steps missing from the markdown", () => {
    const steps = [makeStep("write", "Write draft"), makeStep("extra", "Extra step")];
    const markdown = serializeScenario([makeStep("write", "Write draft")], {
      overview: "",
      stepDescriptions: { write: "desc" },
      notes: "",
    });
    const parsed = parseScenario(markdown, steps);
    expect(parsed.stepDescriptions.extra).toBe("");
  });
});
