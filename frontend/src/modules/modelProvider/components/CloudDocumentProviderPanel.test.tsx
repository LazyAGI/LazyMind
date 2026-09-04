import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";
import CloudDocumentProviderPanel from "./CloudDocumentProviderPanel";

const labels: Record<string, string> = {
  "modelProvider.cloudDocuments.authValid": "认证有效",
  "modelProvider.cloudDocuments.credentialMissing": "待设置凭据",
  "modelProvider.cloudDocuments.authPending": "待授权",
};

function createVm(
  overrides: Partial<CloudDocumentProvidersVm> = {},
): CloudDocumentProvidersVm {
  return {
    t: ((key: string) => labels[key] || key) as CloudDocumentProvidersVm["t"],
    loading: false,
    canCreateLocalSource: false,
    localSourceCount: 0,
    isFeishuAuthValid: false,
    isNotionAuthValid: false,
    isGoogleDriveAuthValid: false,
    isMailConnected: false,
    mailConnectionLabel: "",
    isFeishuSetupReady: true,
    isNotionSetupReady: true,
    validFeishuAccounts: [],
    notionOauthConnection: null,
    googleDriveConnection: null,
    handleManageFeishuAuth: vi.fn(),
    handleManageLocalSource: vi.fn(),
    handleManageGoogleDrive: vi.fn(),
    handleManageMail: vi.fn(),
    handleOpenNotionSetup: vi.fn(),
    ...overrides,
  } as unknown as CloudDocumentProvidersVm;
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

    expect(screen.getAllByText("待设置凭据")).toHaveLength(4);
    expect(screen.queryByText("待授权")).not.toBeInTheDocument();
  });

  it("shows only the valid status for authenticated providers", () => {
    render(
      <CloudDocumentProviderPanel
        vm={createVm({
          isFeishuAuthValid: true,
          isNotionAuthValid: true,
          isGoogleDriveAuthValid: true,
          isMailConnected: true,
        })}
      />,
    );

    expect(screen.getAllByText("认证有效")).toHaveLength(4);
    expect(screen.queryByText("待设置凭据")).not.toBeInTheDocument();
    expect(screen.queryByText("待授权")).not.toBeInTheDocument();
  });
});
