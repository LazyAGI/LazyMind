import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import ChatRiskTip from "./index";

// @/components/ui is a barrel that also re-exports RenderPdf, which pulls in
// react-pdf/pdfjs-dist; that library requires browser canvas APIs (DOMMatrix)
// that jsdom does not implement, so we mock the barrel down to what this
// component actually needs.
vi.mock("@/components/ui", () => ({
  RiskTip: ({ titleKey }: { titleKey: string }) => <span>{titleKey}</span>,
}));

describe("ChatRiskTip", () => {
  it("forwards the chat risk tip translation key to the shared RiskTip component", () => {
    render(<ChatRiskTip />);
    expect(screen.getByText("chat.riskTip")).toBeInTheDocument();
  });
});
