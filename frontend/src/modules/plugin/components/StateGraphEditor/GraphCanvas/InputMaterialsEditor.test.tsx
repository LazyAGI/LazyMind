import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import InputMaterialsEditor from "./InputMaterialsEditor";
import type { SlotDef, StepInput } from "../core/model";

const slots: Record<string, SlotDef> = {
  outline: { id: "outline", type: "text", label: "Outline" },
  body: { id: "body", type: "text" },
};

describe("InputMaterialsEditor", () => {
  it("renders an item per input and shows required/optional status", () => {
    const inputs: StepInput[] = [
      { material: "outline", required: true },
      { material: "body", required: false },
    ];
    renderWithProviders(
      <InputMaterialsEditor
        inputs={inputs}
        slots={slots}
        label="Inputs"
        tip="tip"
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByText("selfEvolutionRun.stateGraphSlotRequired")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.stateGraphSlotOptional")).toBeInTheDocument();
  });

  it("adds a new empty input row when the add button is clicked", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <InputMaterialsEditor
        inputs={[]}
        slots={slots}
        label="Inputs"
        tip="tip"
        onChange={onChange}
      />,
    );

    fireEvent.click(container.querySelector(".npp-material-add-button")!);
    expect(onChange).toHaveBeenCalledWith([{ material: "", required: true }]);
  });

  it("toggles required to optional and clears alternatives", () => {
    const onChange = vi.fn();
    renderWithProviders(
      <InputMaterialsEditor
        inputs={[{ material: "outline", required: true, alternatives: ["body"] }]}
        slots={slots}
        label="Inputs"
        tip="tip"
        onChange={onChange}
      />,
    );

    fireEvent.click(screen.getByText("selfEvolutionRun.stateGraphSlotRequired"));
    expect(onChange).toHaveBeenCalledWith([
      { material: "outline", required: false, alternatives: undefined },
    ]);
  });

  it("removes an input row when its delete button is clicked", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <InputMaterialsEditor
        inputs={[{ material: "outline", required: true }]}
        slots={slots}
        label="Inputs"
        tip="tip"
        onChange={onChange}
      />,
    );

    fireEvent.click(container.querySelector(".npp-material-item .npp-material-icon-button.ant-btn-dangerous")!);
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("disables the add button when readonly", () => {
    const { container } = renderWithProviders(
      <InputMaterialsEditor
        inputs={[]}
        slots={slots}
        readonly
        label="Inputs"
        tip="tip"
        onChange={vi.fn()}
      />,
    );

    expect(container.querySelector(".npp-material-add-button")).toBeDisabled();
  });
});
