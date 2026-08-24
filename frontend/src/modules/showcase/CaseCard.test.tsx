import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import CaseCard from "./CaseCard";

vi.mock("react-i18next", () => ({
  useTranslation: () => {
    const labels: Record<string, string> = {
      "showcase.viewDetail": "查看详情",
      "showcase.try": "试一试",
    };
    return { t: (key: string) => labels[key] || key };
  },
}));

describe("CaseCard", () => {
  it("uses the card for try and keeps the corner action as details in every build", () => {
    render(
      <MemoryRouter>
        <CaseCard item={{
          id: "advisor",
          title: "Advisor",
          description: "Description",
          category: "Education",
          output_type: "report",
          output_label: "Report",
          image_url: "/cover.png",
          result_summary: "Summary",
        } as never} />
      </MemoryRouter>,
    );

    expect(screen.getByRole("link", { name: /Advisor/ })).toHaveAttribute(
      "href",
      "/agent/chat/home?showcase_case=advisor",
    );
    expect(screen.getByRole("link", { name: /查看详情/ })).toHaveAttribute(
      "href",
      "/agent/chat/cases/advisor",
    );
    expect(screen.queryByText("试一试")).not.toBeInTheDocument();
  });
});
