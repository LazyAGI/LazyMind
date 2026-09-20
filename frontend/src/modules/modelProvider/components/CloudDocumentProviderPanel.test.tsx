import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import CloudDocumentProviderPanel from "./CloudDocumentProviderPanel";

const labels: Record<string, string> = {
  "modelProvider.cloudDocuments.authValid": "认证有效",
  "modelProvider.cloudDocuments.credentialMissing": "待设置凭据",
  "modelProvider.cloudDocuments.authPending": "待授权",
  "modelProvider.cloudDocuments.manageAccount": "管理账号",
  "modelProvider.cloudDocuments.notionConnectAction": "新增 Notion 账号",
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
});
