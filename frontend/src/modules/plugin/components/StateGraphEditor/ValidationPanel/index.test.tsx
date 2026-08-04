import { fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import ValidationPanel from "./index";
import type { ValidationError } from "../core/validator";

describe("ValidationPanel", () => {
  it("renders nothing when there are no errors", () => {
    const { container } = renderWithProviders(<ValidationPanel errors={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  // Note: the panel resolves each message via `t(...)`, whose `defaultValue`
  // (err.message) is only used by a real i18n backend for missing keys. The
  // test i18n instance's `parseMissingKeyHandler` always returns the key
  // itself, so assertions target the translation key rather than the raw
  // `message` string passed in.
  it("shows the error count and one item per error", () => {
    const errors: ValidationError[] = [
      { code: "missing_input", message: "Missing input", nodeId: "step1" },
      { code: "other_error", message: "Missing input 2", nodeId: "step2" },
    ];
    renderWithProviders(<ValidationPanel errors={errors} />);
    expect(
      screen.getByText("selfEvolutionRun.validationPanelErrorCount"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("selfEvolutionRun.validationErrors.missing_input"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("selfEvolutionRun.validationErrors.other_error"),
    ).toBeInTheDocument();
  });

  it("calls onSelectNode with the resolved target node id when a locatable error is clicked", () => {
    const onSelectNode = vi.fn();
    const errors: ValidationError[] = [
      { code: "missing_input", message: "Missing input", nodeId: "step1" },
    ];
    renderWithProviders(<ValidationPanel errors={errors} onSelectNode={onSelectNode} />);

    fireEvent.click(screen.getByText("selfEvolutionRun.validationErrors.missing_input"));
    expect(onSelectNode).toHaveBeenCalledWith("step1");
  });

  it("prefers getTargetNodeId over the error's own nodeId when resolving the click target", () => {
    const onSelectNode = vi.fn();
    const getTargetNodeId = vi.fn().mockReturnValue("resolved-node");
    const errors: ValidationError[] = [
      { code: "missing_input", message: "Missing input", nodeId: "step1" },
    ];
    renderWithProviders(
      <ValidationPanel errors={errors} onSelectNode={onSelectNode} getTargetNodeId={getTargetNodeId} />,
    );

    fireEvent.click(screen.getByText("selfEvolutionRun.validationErrors.missing_input"));
    expect(getTargetNodeId).toHaveBeenCalledWith(errors[0]);
    expect(onSelectNode).toHaveBeenCalledWith("resolved-node");
  });

  it("disables the item button and does not call onSelectNode when there is no target node", () => {
    const onSelectNode = vi.fn();
    const errors: ValidationError[] = [{ code: "global_error", message: "Global issue" }];
    renderWithProviders(<ValidationPanel errors={errors} onSelectNode={onSelectNode} />);

    const button = screen.getByText("selfEvolutionRun.validationErrors.global_error");
    expect(button).toBeDisabled();
    fireEvent.click(button);
    expect(onSelectNode).not.toHaveBeenCalled();
  });
});
