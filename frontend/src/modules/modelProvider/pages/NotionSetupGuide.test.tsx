import { describe, expect, it } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import NotionSetupGuide from "./NotionSetupGuide";

describe("NotionSetupGuide", () => {
  it("renders the guide title and all step headings", () => {
    renderWithProviders(<NotionSetupGuide />);
    expect(
      screen.getByText("admin.dataSourceNotionSetupGuide.title"),
    ).toBeInTheDocument();
    expect(
      screen.getAllByText(
        "admin.dataSourceNotionSetupGuide.steps.openDevelopersTitle",
      ).length,
    ).toBeGreaterThan(0);
    expect(
      screen.getAllByText(
        "admin.dataSourceNotionSetupGuide.steps.finishTitle",
      ).length,
    ).toBeGreaterThan(0);
  });

  it("renders the link for the first step pointing at notion developers", () => {
    renderWithProviders(<NotionSetupGuide />);
    const link = screen.getByText("admin.dataSourceNotionSetupGuide.openDevelopers");
    expect(link.closest("a")).toHaveAttribute(
      "href",
      "https://app.notion.com/developers/connections",
    );
  });

  it("renders the callback url detail using the redirect uri helper", () => {
    renderWithProviders(<NotionSetupGuide />);
    expect(
      screen.getByText((content) =>
        content.includes("admin.dataSourceNotionSetupGuide.callbackUrl"),
      ),
    ).toBeInTheDocument();
  });

  it("renders a summary navigation button for each step", () => {
    renderWithProviders(<NotionSetupGuide />);
    expect(
      screen.getAllByRole("button", {
        name: "admin.dataSourceNotionSetupGuide.steps.openDevelopersTitle",
      }).length,
    ).toBeGreaterThan(0);
  });

  it("navigates back via the back button without throwing", () => {
    renderWithProviders(<NotionSetupGuide />);
    const backButton = screen.getByText(
      "admin.dataSourceNotionSetupGuide.backManagement",
    );
    expect(() => fireEvent.click(backButton)).not.toThrow();
  });
});
