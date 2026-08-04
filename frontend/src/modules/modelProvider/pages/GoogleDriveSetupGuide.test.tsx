import { describe, expect, it } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import GoogleDriveSetupGuide from "./GoogleDriveSetupGuide";

describe("GoogleDriveSetupGuide", () => {
  it("renders the guide title and step headings", () => {
    renderWithProviders(<GoogleDriveSetupGuide />);
    expect(
      screen.getByText("admin.dataSourceGoogleDriveSetupGuide.title"),
    ).toBeInTheDocument();
    expect(
      screen.getAllByText(
        "admin.dataSourceGoogleDriveSetupGuide.steps.openConsoleTitle",
      ).length,
    ).toBeGreaterThan(0);
    expect(
      screen.getAllByText(
        "admin.dataSourceGoogleDriveSetupGuide.steps.finishTitle",
      ).length,
    ).toBeGreaterThan(0);
  });

  it("renders a link for the first step pointing at the google cloud console", () => {
    renderWithProviders(<GoogleDriveSetupGuide />);
    const link = screen.getByText(
      "admin.dataSourceGoogleDriveSetupGuide.openConsole",
    );
    expect(link.closest("a")).toHaveAttribute(
      "href",
      "https://console.cloud.google.com/apis/dashboard",
    );
  });

  it("renders a summary navigation button for each step", () => {
    renderWithProviders(<GoogleDriveSetupGuide />);
    expect(
      screen.getAllByRole("button", {
        name: "admin.dataSourceGoogleDriveSetupGuide.steps.openConsoleTitle",
      }).length,
    ).toBeGreaterThan(0);
  });

  it("navigates back via the back button without throwing", () => {
    renderWithProviders(<GoogleDriveSetupGuide />);
    const backButton = screen.getByText(
      "admin.dataSourceGoogleDriveSetupGuide.backTools",
    );
    expect(() => fireEvent.click(backButton)).not.toThrow();
  });
});
