import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import SourceTypeTag from "./SourceTypeTag";

describe("SourceTypeTag", () => {
  it("renders a dash placeholder when no source is provided", () => {
    renderWithProviders(<SourceTypeTag />);
    expect(screen.getByText("-")).toBeInTheDocument();
  });

  it("renders the translation key for the upload source", () => {
    renderWithProviders(<SourceTypeTag source="upload" />);
    expect(screen.getByText("datasetManagement.sourceLabels.upload")).toBeInTheDocument();
  });

  it("renders the translation key for the manual source", () => {
    renderWithProviders(<SourceTypeTag source="manual" />);
    expect(screen.getByText("datasetManagement.sourceLabels.manual")).toBeInTheDocument();
  });

  it("renders the translation key for the flowback source", () => {
    renderWithProviders(<SourceTypeTag source="flowback" />);
    expect(screen.getByText("datasetManagement.sourceLabels.flowback")).toBeInTheDocument();
  });
});
