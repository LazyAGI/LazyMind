import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import {
  FEISHU_OPEN_PLATFORM_URL,
  FeishuCredentialHintAlert,
  getFeishuOpenPlatformAppUrl,
} from "./FeishuCredentialHintAlert";

vi.mock("./feishuOAuth", () => ({
  getFeishuDataSourceCallbackUrl: () => "https://app.example.com/oauth/feishu/callback",
}));

describe("getFeishuOpenPlatformAppUrl", () => {
  it("builds an open platform url for the given app id", () => {
    expect(getFeishuOpenPlatformAppUrl("app-123")).toBe(
      `${FEISHU_OPEN_PLATFORM_URL}/app-123/baseinfo`,
    );
  });

  it("url-encodes special characters in the app id", () => {
    expect(getFeishuOpenPlatformAppUrl("app/1")).toBe(
      `${FEISHU_OPEN_PLATFORM_URL}/app%2F1/baseinfo`,
    );
  });
});

describe("FeishuCredentialHintAlert", () => {
  it("renders nothing when no app id is provided", () => {
    const { container } = renderWithProviders(<FeishuCredentialHintAlert />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the app id is only whitespace", () => {
    const { container } = renderWithProviders(<FeishuCredentialHintAlert appId="   " />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the credential hint alert with a link to the open platform", () => {
    renderWithProviders(<FeishuCredentialHintAlert appId="app-123" />);
    expect(
      screen.getByText((_, element) =>
        Boolean(
          element?.classList.contains("ant-alert-message") &&
            element.textContent?.includes("admin.dataSourceFeishuCredentialHintPrefix"),
        ),
      ),
    ).toBeInTheDocument();
    const link = screen.getByText("admin.dataSourceFeishuOpenPlatformLinkLabel");
    expect(link.closest("a")).toHaveAttribute(
      "href",
      `${FEISHU_OPEN_PLATFORM_URL}/app-123/baseinfo`,
    );
  });
});
