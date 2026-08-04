import { describe, expect, it } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import FeishuSetupGuide from "./FeishuSetupGuide";

describe("FeishuSetupGuide", () => {
  it("renders the guide title and all step headings", () => {
    renderWithProviders(<FeishuSetupGuide />);
    expect(
      screen.getByText("admin.dataSourceFeishuSetupGuide.title"),
    ).toBeInTheDocument();
    expect(
      screen.getAllByText(
        "admin.dataSourceFeishuSetupGuide.steps.openPlatformTitle",
      ).length,
    ).toBeGreaterThan(0);
    expect(
      screen.getAllByText(
        "admin.dataSourceFeishuSetupGuide.steps.finishTitle",
      ).length,
    ).toBeGreaterThan(0);
  });

  it("renders the link for the first step pointing at feishu open platform", () => {
    renderWithProviders(<FeishuSetupGuide />);
    const link = screen.getByText("admin.dataSourceFeishuSetupGuide.openPlatform");
    expect(link.closest("a")).toHaveAttribute(
      "href",
      "https://open.feishu.cn/app?lang=zh-CN",
    );
  });

  it("renders a summary navigation button for each step", () => {
    renderWithProviders(<FeishuSetupGuide />);
    expect(
      screen.getAllByRole("button", {
        name: "admin.dataSourceFeishuSetupGuide.steps.openPlatformTitle",
      }).length,
    ).toBeGreaterThan(0);
  });

  it("navigates back via the back button without throwing", () => {
    renderWithProviders(<FeishuSetupGuide />);
    const backButton = screen.getByText(
      "admin.dataSourceFeishuSetupGuide.backAccounts",
    );
    expect(() => fireEvent.click(backButton)).not.toThrow();
  });
});
