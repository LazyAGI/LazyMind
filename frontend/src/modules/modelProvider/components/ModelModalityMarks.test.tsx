import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ModelModalityMarks } from "./ModelModalityMarks";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

describe("ModelModalityMarks", () => {
  it("draws a distinct logo for each input and output modality", () => {
    render(<ModelModalityMarks providerName="Claude" modelName="claude-opus-4-7" />);

    for (const label of [
      "modelProvider.modality.text",
      "modelProvider.modality.image",
      "modelProvider.modality.file",
    ]) {
      const marks = screen.getAllByLabelText(label);
      expect(marks.length).toBeGreaterThan(0);
      for (const mark of marks) {
        expect(mark.querySelector("svg")).not.toBeNull();
        expect(mark).not.toHaveTextContent(/^[TIAVF]$/);
      }
    }
    expect(screen.getByLabelText("modelProvider.modality.inputAria")).toBeInTheDocument();
    expect(screen.getByLabelText("modelProvider.modality.outputAria")).toBeInTheDocument();
  });
});
