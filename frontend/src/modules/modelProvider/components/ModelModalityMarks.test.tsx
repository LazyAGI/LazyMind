import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ModelModalityMarks } from "./ModelModalityMarks";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("../upstreamCatalog", async () => {
  const { createUpstreamCatalogIndex } = await import("../upstreamCatalogIndex");
  const index = createUpstreamCatalogIndex({
    generated_at: "2026-01-01T00:00:00Z",
    providers: {
      "Fixture Provider": [{
        name: "all-modalities",
        input: ["text", "image", "audio", "video", "file"],
        output: ["text", "image", "audio", "video", "file"],
        context: 32000,
        max_output: 4000,
      }],
    },
  });
  return { lookupUpstreamModel: index.lookupModel };
});

describe("ModelModalityMarks", () => {
  it("draws distinct glyphs with a color class for all five input and output modalities", () => {
    render(<ModelModalityMarks providerName="Fixture Provider" modelName="all-modalities" />);

    for (const direction of ["input", "output"]) {
      const group = screen.getByLabelText(`modelProvider.modality.${direction}Aria`);
      const drawings = [];
      for (const modality of ["text", "image", "audio", "video", "file"]) {
        const mark = within(group).getByLabelText(`modelProvider.modality.${modality}`);
        expect(mark).toHaveClass(`is-${modality}`);
        const svg = mark.querySelector("svg");
        expect(svg).not.toBeNull();
        expect(svg).toHaveAttribute("aria-hidden", "true");
        drawings.push(svg?.innerHTML);
      }
      expect(new Set(drawings).size).toBe(5);
    }
  });

  it("uses the compact layout for selector options", () => {
    const { container } = render(
      <ModelModalityMarks compact providerName="Fixture Provider" modelName="all-modalities" />,
    );

    expect(container.firstChild).toHaveClass("model-modality-marks", "is-compact");
  });

  it("leaves custom models without catalog metadata unmarked", () => {
    const { container } = render(
      <ModelModalityMarks providerName="Fixture Provider" modelName="custom-model" />,
    );

    expect(container).toBeEmptyDOMElement();
  });

  it("honors explicit LLM vision false without changing output or other catalog modalities", () => {
    render(<ModelModalityMarks providerName="Fixture Provider" modelName="all-modalities" modelType="llm" vision={false} />);
    const input = screen.getByLabelText("modelProvider.modality.inputAria");
    expect(within(input).queryByLabelText("modelProvider.modality.image")).not.toBeInTheDocument();
    expect(within(input).getByLabelText("modelProvider.modality.audio")).toBeInTheDocument();
    expect(within(screen.getByLabelText("modelProvider.modality.outputAria")).getByLabelText("modelProvider.modality.image")).toBeInTheDocument();
  });

  it.each([
    { modelType: "llm", vision: true },
    { modelType: "vlm", vision: false },
  ])("shows configured $modelType image input for a model absent from the catalog", ({ modelType, vision }) => {
    render(<ModelModalityMarks providerName="Fixture Provider" modelName="custom-model" modelType={modelType} vision={vision} />);
    expect(within(screen.getByLabelText("modelProvider.modality.inputAria")).getByLabelText("modelProvider.modality.image")).toBeInTheDocument();
  });
});
