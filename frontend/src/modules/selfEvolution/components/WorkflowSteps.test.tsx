import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import {
  AbTestWorkflowStep,
  AnalysisWorkflowStep,
  CodeOptimizeWorkflowStep,
  DatasetWorkflowStep,
  PxReportWorkflowStep,
  WorkflowStepCard,
} from "./WorkflowSteps";
import type { SelfEvolutionWorkflowStep } from "./types";

function makeStep(overrides: Partial<SelfEvolutionWorkflowStep> = {}): SelfEvolutionWorkflowStep {
  return {
    id: "dataset",
    title: "Dataset",
    desc: "Prepare dataset",
    status: "running",
    ...overrides,
  };
}

describe("WorkflowStepCard", () => {
  it("renders the step title, description and status label", () => {
    renderWithProviders(
      <WorkflowStepCard step={makeStep()} index={0} statusLabel="Running" />,
    );
    expect(screen.getByText("Dataset")).toBeInTheDocument();
    expect(screen.getByText("Prepare dataset")).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
  });

  it("renders progress phases when provided", () => {
    const step = makeStep({
      progressPhases: [
        { id: "rag", title: "RAG", desc: "retrieval", statusText: "running", percent: 40 },
      ],
    });
    renderWithProviders(<WorkflowStepCard step={step} index={0} statusLabel="Running" />);
    expect(screen.getByText("RAG")).toBeInTheDocument();
    expect(screen.getByText("40%")).toBeInTheDocument();
  });

  it("falls back to a simple progress bar when there are no phases", () => {
    const step = makeStep({ progress: { statusText: "half", percent: 50 } });
    renderWithProviders(<WorkflowStepCard step={step} index={0} statusLabel="Running" />);
    expect(screen.getByText("50%")).toBeInTheDocument();
  });

  it("prefers a custom runtimeSummary node over the plain runtimeText", () => {
    const step = makeStep({ runtimeText: "plain text" });
    renderWithProviders(
      <WorkflowStepCard step={step} index={0} statusLabel="Running" runtimeSummary={<span>custom summary</span>} />,
    );
    expect(screen.getByText("custom summary")).toBeInTheDocument();
    expect(screen.queryByText("plain text")).not.toBeInTheDocument();
  });
});

describe("DatasetWorkflowStep", () => {
  it("uses the primary download url when available", () => {
    const onDownload = vi.fn();
    renderWithProviders(
      <DatasetWorkflowStep
        downloadUrl="https://example.com/data.json"
        fallbackDownloadUrl="https://example.com/fallback.json"
        fileName="dataset.json"
        getDownloadFileName={(url, fallbackName) => (url ? "dataset.json" : fallbackName)}
        onDownload={onDownload}
      />,
    );
    const link = screen.getByText("selfEvolutionRun.downloadView");
    expect(link).toHaveAttribute("href", "https://example.com/data.json");
    fireEvent.click(link);
    expect(onDownload).toHaveBeenCalledTimes(1);
  });

  it("falls back to the fallback url when the primary is empty", () => {
    renderWithProviders(
      <DatasetWorkflowStep
        downloadUrl=""
        fallbackDownloadUrl="https://example.com/fallback.json"
        fileName="dataset.json"
        getDownloadFileName={(_url, fallbackName) => fallbackName}
        onDownload={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.downloadView")).toHaveAttribute(
      "href",
      "https://example.com/fallback.json",
    );
  });
});

describe("PxReportWorkflowStep", () => {
  it("shows the single-category label when isSingleCategory is true", () => {
    renderWithProviders(
      <PxReportWorkflowStep
        categoryCount={1}
        isSingleCategory
        downloadUrl="https://example.com/report.json"
        onCollapseChange={vi.fn()}
        onDownload={vi.fn()}
        getDownloadFileName={() => "eval-report.json"}
      >
        <div>report body</div>
      </PxReportWorkflowStep>,
    );
    expect(screen.getByText("selfEvolutionRun.viewEvalChartSingle")).toBeInTheDocument();
  });

  it("shows the generic label when there are zero categories", () => {
    renderWithProviders(
      <PxReportWorkflowStep
        categoryCount={0}
        isSingleCategory={false}
        downloadUrl=""
        onCollapseChange={vi.fn()}
        onDownload={vi.fn()}
        getDownloadFileName={() => "eval-report.json"}
      >
        <div>report body</div>
      </PxReportWorkflowStep>,
    );
    expect(screen.getByText("selfEvolutionRun.viewEvalChart")).toBeInTheDocument();
  });
});

describe("AnalysisWorkflowStep", () => {
  it("renders the collapse label and forwards collapse changes", () => {
    const onCollapseChange = vi.fn();
    renderWithProviders(
      <AnalysisWorkflowStep onCollapseChange={onCollapseChange}>
        <div>analysis body</div>
      </AnalysisWorkflowStep>,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.viewFullAnalysisReport"));
    expect(onCollapseChange).toHaveBeenCalled();
  });
});

describe("CodeOptimizeWorkflowStep", () => {
  it("renders the download link with the code diff filename", () => {
    renderWithProviders(
      <CodeOptimizeWorkflowStep
        downloadUrl="https://example.com/diff.patch"
        onCollapseChange={vi.fn()}
        onDownload={vi.fn()}
        getDownloadFileName={() => "code-diff.diff"}
      >
        <div>diff body</div>
      </CodeOptimizeWorkflowStep>,
    );
    expect(screen.getByText("selfEvolutionRun.downloadView")).toHaveAttribute(
      "download",
      "code-diff.diff",
    );
  });
});

describe("AbTestWorkflowStep", () => {
  it("uses the fallback download url when the primary url is empty", () => {
    renderWithProviders(
      <AbTestWorkflowStep
        downloadUrl=""
        fallbackDownloadUrl="https://example.com/ab.json"
        onCollapseChange={vi.fn()}
        onDownload={vi.fn()}
        getDownloadFileName={() => "ab-test-comparison.json"}
      >
        <div>ab body</div>
      </AbTestWorkflowStep>,
    );
    expect(screen.getByText("selfEvolutionRun.downloadView")).toHaveAttribute(
      "href",
      "https://example.com/ab.json",
    );
  });
});
