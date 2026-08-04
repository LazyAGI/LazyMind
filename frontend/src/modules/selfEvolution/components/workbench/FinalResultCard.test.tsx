import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { FinalResultCard } from "./FinalResultCard";
import type { SelfEvolutionFinalResultSummary } from "./types";

function makeSummary(overrides: Partial<SelfEvolutionFinalResultSummary> = {}): SelfEvolutionFinalResultSummary {
  return {
    verdict: "accept",
    title: "Improved",
    desc: "The optimized version performs better.",
    metrics: [{ label: "Latency", value: "-10%", tone: "good" }],
    reasons: ["Latency improved"],
    ...overrides,
  };
}

describe("FinalResultCard", () => {
  it("renders a loading state when no summary is provided", () => {
    renderWithProviders(<FinalResultCard finalResultSummary={undefined} onOpenArtifact={vi.fn()} />);
    expect(screen.getByText("selfEvolutionRun.finalResultLoading")).toBeInTheDocument();
    expect(document.querySelector(".self-evolution-final-result.is-loading")).toBeInTheDocument();
  });

  it("renders the title, description, metrics and reasons for a resolved summary", () => {
    renderWithProviders(
      <FinalResultCard finalResultSummary={makeSummary()} onOpenArtifact={vi.fn()} />,
    );
    expect(screen.getByText("Improved")).toBeInTheDocument();
    expect(screen.getByText("The optimized version performs better.")).toBeInTheDocument();
    expect(screen.getByText("Latency")).toBeInTheDocument();
    expect(screen.getByText("-10%")).toBeInTheDocument();
    expect(screen.getByText("Latency improved")).toBeInTheDocument();
  });

  it("applies the reject verdict class and calls onOpenArtifact with abtests when clicking the action", () => {
    const onOpenArtifact = vi.fn();
    renderWithProviders(
      <FinalResultCard finalResultSummary={makeSummary({ verdict: "reject" })} onOpenArtifact={onOpenArtifact} />,
    );
    expect(document.querySelector(".self-evolution-final-result.is-reject")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.viewABTestDetail"));
    expect(onOpenArtifact).toHaveBeenCalledWith("abtests");
  });

  it("omits the metrics and reasons sections when they are empty", () => {
    renderWithProviders(
      <FinalResultCard
        finalResultSummary={makeSummary({ metrics: [], reasons: [] })}
        onOpenArtifact={vi.fn()}
      />,
    );
    expect(document.querySelector(".self-evolution-final-result-metrics")).not.toBeInTheDocument();
    expect(document.querySelector(".self-evolution-final-result-reasons")).not.toBeInTheDocument();
  });
});
