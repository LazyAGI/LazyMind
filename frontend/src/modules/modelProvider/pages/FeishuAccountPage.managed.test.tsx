import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import FeishuAccountPage from "./FeishuAccountPage";

const mocks = vi.hoisted(() => ({
  addAccount: vi.fn(),
  openAccountModal: vi.fn(),
}));

vi.mock("../components/feishu/FeishuAccountTable", () => ({
  default: () => null,
}));

vi.mock("../components/feishu/FeishuAccountFormModal", () => ({
  default: () => null,
}));

vi.mock("../hooks/useFeishuAccounts", () => ({
  useFeishuAccounts: () => ({
    t: (key: string) => ({
      "modelProvider.cloudDocuments.backToProviders": "返回云文档",
      "modelProvider.cloudDocuments.feishuAccountManagementTitle": "飞书账号",
      "modelProvider.cloudDocuments.feishuAccountManagementSubtitle": "管理飞书账号",
      "modelProvider.cloudDocuments.feishuSetupGuideAction": "查看飞书接入教程",
      "modelProvider.cloudDocuments.feishuAccountCreate": "新增飞书账号",
      "modelProvider.cloudDocuments.feishuAccountAdvancedSetup": "高级 BYO 配置",
      "modelProvider.cloudDocuments.feishuSetupCardTitle": "OAuth 回调配置",
      "modelProvider.cloudDocuments.feishuAccountSecurityHint": "凭据安全提示",
      "modelProvider.cloudDocuments.feishuCallbackLabel": "Callback URL",
      "modelProvider.cloudDocuments.feishuAccountOpenPlatform": "飞书开放平台",
    })[key] || key,
    form: {},
    callbackUrl: "http://127.0.0.1/oauth/feishu/callback",
    accounts: [],
    accountsLoading: false,
    modalOpen: false,
    editingAccountId: null,
    submitting: false,
    manualOauthModalOpen: false,
    manualOauthCallbackValue: "",
    manualOauthSubmitting: false,
    setModalOpen: vi.fn(),
    setEditingAccountId: vi.fn(),
    setManualOauthModalOpen: vi.fn(),
    setManualOauthCallbackValue: vi.fn(),
    openAccountModal: mocks.openAccountModal,
    handleAddAccount: mocks.addAccount,
    addingAccount: false,
    handleSaveAccount: vi.fn(),
    handleAuthorizeAccount: vi.fn(),
    handleDeleteAccount: vi.fn(),
    handleToggleChat: vi.fn(),
    handleSubmitManualOauthCallback: vi.fn(),
  }),
}));

describe("FeishuAccountPage managed account creation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("uses one add-account action that selects the flow", () => {
    render(
      <MemoryRouter>
        <FeishuAccountPage />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: /新增飞书账号/ }));

    expect(mocks.addAccount).toHaveBeenCalledOnce();
    expect(mocks.openAccountModal).not.toHaveBeenCalled();
  });

  it("does not expose a separate advanced BYO action", () => {
    render(<MemoryRouter><FeishuAccountPage /></MemoryRouter>);
    expect(screen.queryByRole("button", { name: /高级 BYO 配置/ })).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /新增飞书账号/ })).toHaveLength(1);
  });
});
