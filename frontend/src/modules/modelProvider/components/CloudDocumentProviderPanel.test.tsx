import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import CloudDocumentProviderPanel from "./CloudDocumentProviderPanel";

const labels: Record<string, string> = {
  "modelProvider.cloudDocuments.authValid": "认证有效",
  "modelProvider.cloudDocuments.credentialMissing": "待设置凭据",
  "modelProvider.cloudDocuments.authPending": "待授权",
  "modelProvider.cloudDocuments.manageAccount": "管理账号",
  "modelProvider.cloudDocuments.notionConnectAction": "新增 Notion 账号",
  "modelProvider.cloudDocuments.obsidianTitle": "Obsidian",
  "modelProvider.cloudDocuments.obsidianSetupGuideAction": "查看 Obsidian 接入教程",
  "modelProvider.cloudDocuments.obsidianDeploymentStatus": "按部署配置",
  "modelProvider.cloudDocuments.obsidianEnvironmentHint": "本地 Markdown 笔记，运行 LazyMind 的机器需能访问笔记库",
  "modelProvider.cloudDocuments.obsidianUnavailableHint": "选择 Obsidian 根目录",
  "modelProvider.cloudDocuments.obsidianConnectedStatus": "已连接",
  "modelProvider.cloudDocuments.obsidianUnavailableStatus": "未配置",
  "modelProvider.cloudDocuments.obsidianChooseRoot": "选择根目录",
  "modelProvider.cloudDocuments.obsidianChangeRoot": "更换根目录",
  "modelProvider.cloudDocuments.obsidianDisconnect": "断开连接",
  "modelProvider.cloudDocuments.obsidianUpdating": "正在更新 Obsidian 连接…",
};

function createVm(overrides: Record<string, unknown> = {}) {
  return {
    t: (key: string) => labels[key] || key,
    loading: false,
    canCreateLocalSource: false,
    localSourceCount: 0,
    isFeishuAuthValid: false,
    isNotionAuthValid: false,
    isGitHubAuthValid: false,
    isGoogleDriveAuthValid: false,
    isWeChatOfficialAccountAuthValid: false,
    hasWeChatOfficialAccount: false,
    isMailAuthValid: false,
    mailAccounts: [],
    isFeishuSetupReady: true,
    isNotionSetupReady: true,
    isGitHubSetupReady: true,
    validFeishuAccounts: [],
    notionOauthConnection: null,
    googleDriveConnection: null,
    handleManageFeishuAuth: vi.fn(),
    handleManageLocalSource: vi.fn(),
    handleManageGoogleDrive: vi.fn(),
    handleManageWeChatOfficialAccount: vi.fn(),
    handleManageMail: vi.fn(),
    handleManageNotionAuth: vi.fn(),
    handleOpenNotionSetup: vi.fn(),
    handleOpenGitHubSetup: vi.fn(),
    ...overrides,
  } as never;
}

describe("CloudDocumentProviderPanel", () => {
  it("does not show the local document directory count", () => {
    render(
      <CloudDocumentProviderPanel
        vm={createVm({ canCreateLocalSource: true, localSourceCount: 42 })}
      />,
    );

    expect(screen.queryByText("42")).not.toBeInTheDocument();
    expect(screen.queryByText("个目录")).not.toBeInTheDocument();
  });

  it("shows only the missing-credentials status for unverified providers", () => {
    render(<CloudDocumentProviderPanel vm={createVm()} />);

    expect(screen.getAllByText("待设置凭据")).toHaveLength(6);
    expect(screen.queryByText("待授权")).not.toBeInTheDocument();
  });

  it("shows only the valid status for authenticated providers", () => {
    render(
      <CloudDocumentProviderPanel
        vm={createVm({
          isFeishuAuthValid: true,
          isNotionAuthValid: true,
          isGitHubAuthValid: true,
          isGoogleDriveAuthValid: true,
          isWeChatOfficialAccountAuthValid: true,
          isMailAuthValid: true,
          mailAccounts: ["mail@example.com"],
        })}
      />,
    );

    expect(screen.getAllByText("认证有效")).toHaveLength(6);
    expect(screen.queryByText("待设置凭据")).not.toBeInTheDocument();
    expect(screen.queryByText("待授权")).not.toBeInTheDocument();
  });

  it("keeps a configured but unverified WeChat account pending", () => {
    render(
      <CloudDocumentProviderPanel
        vm={createVm({ hasWeChatOfficialAccount: true })}
      />,
    );

    expect(screen.getAllByText("待设置凭据")).toHaveLength(5);
    expect(screen.getByText("待授权")).toBeInTheDocument();
  });

  it("reauthorizes the existing Notion connection from Manage account", () => {
    const handleManageNotionAuth = vi.fn();
    const handleOpenNotionSetup = vi.fn();
    render(
      <CloudDocumentProviderPanel
        vm={createVm({
          isNotionAuthValid: true,
          handleManageNotionAuth,
          handleOpenNotionSetup,
        })}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /管理账号/ }));

    expect(handleManageNotionAuth).toHaveBeenCalledOnce();
    expect(handleOpenNotionSetup).not.toHaveBeenCalled();
  });

  it("starts a new Notion connection only when none exists", () => {
    const handleManageNotionAuth = vi.fn();
    const handleOpenNotionSetup = vi.fn();
    render(
      <CloudDocumentProviderPanel
        vm={createVm({
          isNotionAuthValid: false,
          handleManageNotionAuth,
          handleOpenNotionSetup,
        })}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /新增 Notion 账号/ }));

    expect(handleOpenNotionSetup).toHaveBeenCalledOnce();
    expect(handleManageNotionAuth).not.toHaveBeenCalled();
  });

  it("keeps the setup guide visible alongside Desktop directory controls", async () => {
    const handleManageObsidian = vi.fn();
    const handleDisconnectObsidian = vi.fn();
    const { rerender } = render(
      <CloudDocumentProviderPanel
        vm={createVm({
          isDesktopRuntime: true,
          obsidianConfig: { configured: true, available: true, root: "/tmp/obs" },
          handleManageObsidian,
          handleDisconnectObsidian,
        })}
      />,
    );

    expect(screen.getByText("Obsidian")).toBeInTheDocument();
    const cardGuide = screen.getByRole("link", { name: "查看 Obsidian 接入教程" });
    expect(cardGuide).toHaveAttribute("href", "/cloud-documents/docs/obsidian-setup");
    expect(cardGuide).toHaveAttribute("target", "_blank");
    expect(cardGuide).toHaveAttribute("rel", "noopener noreferrer");

    fireEvent.click(screen.getByRole("button", { name: /更换根目录/ }));
    const obsidianGuide = within(screen.getByRole("dialog")).getByRole("link", { name: "查看 Obsidian 接入教程" });
    expect(obsidianGuide).toHaveAttribute("href", "/cloud-documents/docs/obsidian-setup");
    expect(obsidianGuide).toHaveAttribute("target", "_blank");
    expect(obsidianGuide).toHaveAttribute("rel", "noreferrer");
    fireEvent.click(screen.getByRole("button", { name: "断开连接" }));
    expect(handleDisconnectObsidian).toHaveBeenCalledTimes(1);

    rerender(
      <CloudDocumentProviderPanel
        vm={createVm({
          isDesktopRuntime: true,
          obsidianConfig: { configured: false, available: false },
          handleManageObsidian,
        })}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /选择根目录/ }));
    fireEvent.click(screen.getByRole("button", { name: "选择根目录" }));
    expect(handleManageObsidian).toHaveBeenCalledTimes(1);
    expect(within(screen.getByRole("dialog")).getByRole("link", { name: "查看 Obsidian 接入教程" })).toBeInTheDocument();
  });

  it("shows server-verified readiness on the web without offering native controls", () => {
    render(<CloudDocumentProviderPanel vm={createVm({
      isDesktopRuntime: false,
      obsidianConfig: { configured: true, available: true, root: "/tmp/stale-desktop-root" },
    })} />);
    expect(screen.getByRole("heading", { name: "Obsidian" })).toBeInTheDocument();
    expect(screen.getByText("已连接")).toBeInTheDocument();
    expect(screen.getByText(/本地 Markdown 笔记/)).toBeInTheDocument();

    expect(screen.queryByRole("button", { name: /选择根目录|更换根目录/ })).not.toBeInTheDocument();
    const guide = screen.getByRole("link", { name: "查看 Obsidian 接入教程" });
    expect(guide).toHaveAttribute("href", "/cloud-documents/docs/obsidian-setup");
    expect(guide).toHaveAttribute("target", "_blank");
    expect(guide).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("passes a modal-close callback to the native Obsidian selection flow", () => {
    const handleManageObsidian = vi.fn();
    render(
      <CloudDocumentProviderPanel
        vm={createVm({
          isDesktopRuntime: true,
          obsidianConfig: { configured: false, available: false },
          handleManageObsidian,
        })}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /选择根目录/ }));
    expect(within(screen.getByRole("dialog")).getByRole("link", { name: "查看 Obsidian 接入教程" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "选择根目录" }));

    expect(handleManageObsidian).toHaveBeenCalledWith(expect.any(Function));
    expect(within(screen.getByRole("dialog")).getByRole("link", { name: "查看 Obsidian 接入教程" })).toBeInTheDocument();
  });

  it("shows a blocking progress modal while the Obsidian runtime is restarting", () => {
    render(
      <CloudDocumentProviderPanel
        vm={createVm({
          isDesktopRuntime: true,
          obsidianLoading: true,
          obsidianConfig: { configured: true, available: true, root: "/tmp/obs" },
        })}
      />,
    );

    expect(screen.getByText("正在更新 Obsidian 连接…")).toBeInTheDocument();
  });
});
