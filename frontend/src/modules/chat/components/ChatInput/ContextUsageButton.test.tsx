import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ContextUsageButton from "./ContextUsageButton";
import type { ContextUsageReport } from "../../utils/request";

const { estimate } = vi.hoisted(() => ({ estimate: vi.fn() }));
vi.mock("../../utils/request", () => ({ estimateContextUsage: estimate, exportContextPrompt: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({
  t: (key: string, options?: { services?: string }) => options?.services ? `${key}: ${options.services}` : key,
}) }));
const report: ContextUsageReport = {
  scope: "next_request", estimated_tokens: 100, categories: [], estimation_version: "1",
  preview_accuracy: "llm_enhanced",
};
const openReport = () => {
  render(<ContextUsageButton staleKey="s" resetKey="r" buildRequest={() => ({})} />);
  fireEvent.click(screen.getByRole("button", { name: "chat.contextUsageShow" }));
};

describe("context usage catalog disclosure", () => {
  beforeEach(() => estimate.mockReset());
  it("warns about missing service labels while retaining model preview accuracy", async () => {
    estimate.mockResolvedValue({ ...report, mcp_catalog: {
      source: "discovered_snapshot", complete: false, missing_services: ["Personal Notion", "Team Drive"],
    } });
    openReport();
    expect(await screen.findByText("chat.contextUsageMcpIncomplete: Personal Notion, Team Drive")).toBeInTheDocument();
    expect(screen.getByText("chat.contextUsageMcpSnapshot")).toBeInTheDocument();
    expect(screen.getByText("chat.contextUsageLlmEnhanced")).toBeInTheDocument();
    expect(estimate).toHaveBeenCalledOnce();
    expect(estimate).toHaveBeenCalledWith({ context_preview_allow_llm_routing: false });
  });
  it("identifies a complete stored snapshot without an incomplete warning", async () => {
    estimate.mockResolvedValue({ ...report, mcp_catalog: {
      source: "discovered_snapshot", complete: true, missing_services: [],
    } });
    openReport();
    expect(await screen.findByText("chat.contextUsageMcpSnapshot")).toBeInTheDocument();
    expect(screen.queryByText(/chat.contextUsageMcpIncomplete/)).toBeNull();
  });
  it("keeps older reports without catalog metadata working", async () => {
    estimate.mockResolvedValue(report);
    openReport();
    expect(await screen.findByText("chat.contextUsageLlmEnhanced")).toBeInTheDocument();
    expect(screen.queryByText("chat.contextUsageMcpSnapshot")).toBeNull();
    expect(screen.queryByText(/chat.contextUsageMcpIncomplete/)).toBeNull();
  });
});
