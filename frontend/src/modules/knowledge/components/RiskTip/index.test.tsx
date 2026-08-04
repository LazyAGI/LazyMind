import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import KnowledgeRiskTip from "./index";

vi.mock("@/components/ui", () => ({
  RiskTip: ({ titleKey }: { titleKey: string }) => <div data-testid="risk-tip">{titleKey}</div>,
}));

describe("KnowledgeRiskTip", () => {
  it("renders the shared RiskTip with the knowledge upload security title key", () => {
    render(<KnowledgeRiskTip />);
    expect(screen.getByTestId("risk-tip")).toHaveTextContent(
      "knowledge.uploadSecurityRiskTip",
    );
  });
});
