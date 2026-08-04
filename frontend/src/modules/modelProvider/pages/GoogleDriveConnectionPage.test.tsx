import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import GoogleDriveConnectionPage from "./GoogleDriveConnectionPage";

vi.mock("@/modules/modelProvider/components/GoogleDriveConnectionSection", () => ({
  default: () => <div data-testid="google-drive-connection-section" />,
}));

vi.mock("@/modules/dataSource/oauth/urls", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/modules/dataSource/oauth/urls")>();
  return {
    ...actual,
    getCloudDataSourceCallbackUrl: () => "https://app.example.com/oauth/googledrive/data-source/callback",
  };
});

describe("GoogleDriveConnectionPage", () => {
  it("renders the page title and callback url", () => {
    renderWithProviders(<GoogleDriveConnectionPage />);
    expect(
      screen.getByText("admin.dataSourceGoogleDrivePageTitle"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("https://app.example.com/oauth/googledrive/data-source/callback"),
    ).toBeInTheDocument();
  });

  it("renders the connection section", () => {
    renderWithProviders(<GoogleDriveConnectionPage />);
    expect(
      screen.getByTestId("google-drive-connection-section"),
    ).toBeInTheDocument();
  });

  it("shows the https hint when the callback url is supported", () => {
    renderWithProviders(<GoogleDriveConnectionPage />);
    expect(
      screen.getByText("admin.dataSourceGoogleDriveHttpsHint"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("admin.dataSourceGoogleDriveInvalidCallbackTitle"),
    ).not.toBeInTheDocument();
  });

  it("navigates back via the back button without throwing", () => {
    renderWithProviders(<GoogleDriveConnectionPage />);
    const backButton = screen.getByText("admin.dataSourceGoogleDriveBackProviders");
    expect(() => fireEvent.click(backButton)).not.toThrow();
  });
});
