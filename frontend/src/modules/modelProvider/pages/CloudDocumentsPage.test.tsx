import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enUS from "../../../i18n/locales/en-US";
import zhCN from "../../../i18n/locales/zh-CN";
import CloudDocumentsPage from "./CloudDocumentsPage";
import { cloudDocumentLoginReturnPath } from "../utils/cloudDocumentUrls";

const mocks = vi.hoisted(() => ({
  vm: {} as Record<string, unknown>,
  getConnection: vi.fn(),
  authorize: vi.fn(),
  enable: vi.fn(),
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: { getConnectionApiAuthserviceV1CloudConnectionsConnectionIdGet: mocks.getConnection },
}));
vi.mock("@/modules/dataSource/oauth/api", () => ({
  requestCloudDataSourceAuthorizeUrl: mocks.authorize,
  enableCloudConnectionForChat: mocks.enable,
}));

const labels: Record<string, string> = {
  "modelProvider.cloudDocuments.title": "云文档",
  "modelProvider.cloudDocuments.subtitle": "统一管理云文档",
  "modelProvider.cloudDocuments.onboardingEntry": "新手指引",
  "modelProvider.cloudDocuments.overview": "接入概览",
  "modelProvider.cloudDocuments.providerReadySuffix": "类资源已就绪",
  "modelProvider.cloudDocuments.useCasesAria": "云文档使用场景",
  "modelProvider.cloudDocuments.chatUseCaseTitle": "文档对话",
  "modelProvider.cloudDocuments.chatUseCaseDescription": "在对话中操作云文档",
  "modelProvider.cloudDocuments.chatUseCaseCapabilityNote": "能力说明",
  "modelProvider.cloudDocuments.chatCapabilityHelp": "查看能力说明",
  "modelProvider.cloudDocuments.knowledgeUseCaseTitle": "知识库同步",
  "modelProvider.cloudDocuments.knowledgeUseCaseDescription": "定期同步",
  "modelProvider.cloudDocuments.goChat": "去对话使用",
  "modelProvider.cloudDocuments.goKnowledge": "前往知识库",
  "modelProvider.cloudDocuments.onboardingTitle": "云文档新手指引",
  "modelProvider.cloudDocuments.onboardingStepBadge": "2 步完成入门",
  "modelProvider.cloudDocuments.onboardingHeading": "完成认证，选择你的使用方式",
  "modelProvider.cloudDocuments.onboardingConnectTitle": "完成云文档账号认证",
  "modelProvider.cloudDocuments.onboardingConnectDescription": "选择云文档并配置",
  "modelProvider.cloudDocuments.onboardingUseTitle": "开始使用云文档能力",
  "modelProvider.cloudDocuments.onboardingUseDescription": "在对话或知识库中使用",
  "modelProvider.cloudDocuments.onboardingCurrent": "现在进行",
  "modelProvider.cloudDocuments.onboardingComplete": "已完成",
  "modelProvider.cloudDocuments.onboardingLocked": "认证后可用",
  "modelProvider.cloudDocuments.onboardingUnlocked": "已解锁",
  "modelProvider.cloudDocuments.onboardingPartiallyUnlocked": "部分可用",
  "modelProvider.cloudDocuments.onboardingPrimaryAction": "开始第 1 步：去认证",
  "modelProvider.cloudDocuments.onboardingLater": "稍后再说",
  "modelProvider.cloudDocuments.connectAnotherSource": "连接其他云文档",
  "modelProvider.cloudDocuments.guideChatCapability": "在对话中引用云文档",
  "modelProvider.cloudDocuments.guideKnowledgeCapability": "创建知识库并定时同步",
  "modelProvider.cloudDocuments.guideKnowledgeUnavailable": "知识库同步（暂不支持）",
  "modelProvider.cloudDocuments.sourceChoiceTitle": "选择云文档",
  "modelProvider.cloudDocuments.sourceChoiceStep": "第 1 步 · 选择云文档",
  "modelProvider.cloudDocuments.sourceChoiceHeading": "选择要连接的云文档",
  "modelProvider.cloudDocuments.sourceChoiceDescription": "选择后进入配置页面",
  "modelProvider.cloudDocuments.sourceChoicePrevious": "返回上一步",
  "modelProvider.cloudDocuments.guideSource.local.title": "本地文档",
  "modelProvider.cloudDocuments.guideSource.local.description": "配置目录",
  "modelProvider.cloudDocuments.guideSource.feishu.title": "飞书",
  "modelProvider.cloudDocuments.guideSource.feishu.description": "配置 App",
  "modelProvider.cloudDocuments.guideSource.notion.title": "Notion",
  "modelProvider.cloudDocuments.guideSource.notion.description": "配置 OAuth",
  "modelProvider.cloudDocuments.guideSource.github.title": "GitHub",
  "modelProvider.cloudDocuments.guideSource.github.description": "配置 GitHub OAuth",
  "modelProvider.cloudDocuments.guideSource.googledrive.title": "Google Drive",
  "modelProvider.cloudDocuments.guideSource.googledrive.description": "配置 Google OAuth",
  "modelProvider.cloudDocuments.connectionSuccessTitle": "连接成功",
  "modelProvider.cloudDocuments.connectionSuccessHeading": "已连接成功。请选择一种方式开始使用：",
  "modelProvider.cloudDocuments.connectionSuccessDescription": "能力已解锁",
  "modelProvider.cloudDocuments.connectionSuccessGoogleDescription": "仅支持对话只读",
  "modelProvider.cloudDocuments.successChat.notion.title": "在对话中读取 Notion 文档",
  "modelProvider.cloudDocuments.successChat.notion.description": "基于 Notion 问答",
  "modelProvider.cloudDocuments.successKnowledgeTitle": "去创建云文档知识库",
  "modelProvider.cloudDocuments.successKnowledge.notion.description": "使用刚连接的 Notion",
  "modelProvider.cloudDocuments.onboardingGotIt": "我知道了",
};

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => labels[key] || key,
  }),
}));

vi.mock("../hooks/useCloudDocumentProviders", () => ({
  useCloudDocumentProviders: () => mocks.vm,
}));

vi.mock("../constants/cloudProviderOptions", () => ({
  cloudProviderOptions: [
    { type: "local", icon: null },
    { type: "feishu", icon: null },
    { type: "notion", icon: null },
    { type: "github", icon: null },
    { type: "googledrive", icon: null },
  ],
  cloudAuthProviderOptions: [
    { type: "feishu" },
    { type: "notion" },
    { type: "github" },
    { type: "googledrive" },
  ],
}));

vi.mock("../components/CloudDocumentProviderPanel", () => ({
  default: () => <div>provider-list</div>,
  CloudDocumentModals: () => null,
}));

function renderPage(entry = "/cloud-documents") {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <CloudDocumentsPage />
    </MemoryRouter>,
  );
}

describe("CloudDocumentsPage onboarding", () => {
  it("preserves only cloud document hub links through login", () => {
    const path = "/cloud-documents?provider=notion&connection_id=target";
    expect(cloudDocumentLoginReturnPath(path)).toBe(path);
    for (const unsafe of ["https://evil.test", "//evil.test", "/cloud-documents/../login", "/cloud-documents\\evil", {}, undefined]) {
      expect(cloudDocumentLoginReturnPath(unsafe)).toBeUndefined();
    }
  });
  it("keeps cloud document copy free of data-source terminology", () => {
    const zhCloudDocumentCopy = JSON.stringify({
      page: zhCN.modelProvider.cloudDocuments,
      feishuGuide: zhCN.admin.dataSourceFeishuSetupGuide,
      notionGuide: zhCN.admin.dataSourceNotionSetupGuide,
      googleDriveGuide: zhCN.admin.dataSourceGoogleDriveSetupGuide,
      googleDriveBack: zhCN.admin.dataSourceGoogleDriveBackProviders,
      feishuDelete: zhCN.admin.dataSourceFeishuAccountDeleteContent,
    });
    const enCloudDocumentCopy = JSON.stringify({
      page: enUS.modelProvider.cloudDocuments,
      feishuGuide: enUS.admin.dataSourceFeishuSetupGuide,
      notionGuide: enUS.admin.dataSourceNotionSetupGuide,
      googleDriveGuide: enUS.admin.dataSourceGoogleDriveSetupGuide,
      googleDriveBack: enUS.admin.dataSourceGoogleDriveBackProviders,
      feishuDelete: enUS.admin.dataSourceFeishuAccountDeleteContent,
    }).toLowerCase();

    expect(zhCloudDocumentCopy).not.toContain("数据源");
    expect(enCloudDocumentCopy).not.toContain("data source");
  });

  beforeEach(() => {
    mocks.getConnection.mockReset();
    mocks.authorize.mockReset();
    mocks.enable.mockReset();
    window.localStorage.clear();
    window.sessionStorage.clear();
    mocks.vm = {
      loading: false,
      canCreateLocalSource: true,
      localSourceCount: 0,
      isFeishuAuthValid: false,
      isNotionAuthValid: false,
      isGitHubAuthValid: false,
      isGoogleDriveAuthValid: false,
      isMailAuthValid: false,
      handleManageLocalSource: vi.fn(),
      handleManageFeishuAuth: vi.fn(),
      handleManageGoogleDrive: vi.fn(),
      handleManageNotionAuth: vi.fn(),
      handleOpenNotionSetup: vi.fn(),
      handleOpenGitHubSetup: vi.fn(),
    };
  });

  it.each(["feishu", "notion", "googledrive"])("reauthorizes the exact %s connection without automatically starting OAuth", async (provider) => {
    mocks.getConnection.mockResolvedValue({ data: { connection_id: "target", provider, auth_mode: "oauth_user", display_name: "Target account", status: "EXPIRED", provider_options: { chat_enabled: false } } });
    // Reject before navigation so this test exercises the real click/request boundary.
    mocks.authorize.mockRejectedValue(new Error("offline"));
    renderPage(`/cloud-documents?provider=${provider}&connection_id=target`);
    await screen.findByText("Target account · EXPIRED");
    expect(mocks.getConnection).toHaveBeenCalledWith({ connectionId: "target" });
    expect(mocks.authorize).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("modelProvider.cloudDocuments.recoveryAuthorize"));
    await waitFor(() => expect(mocks.authorize).toHaveBeenCalledWith(provider, {
      tenantId: "", scopes: [], reauthorizeConnectionId: "target", returnUrl: window.location.href,
    }));
    await screen.findByText("modelProvider.cloudDocuments.recoveryUnavailable");
    fireEvent.click(screen.getByText("modelProvider.cloudDocuments.recoveryEnable"));
    await waitFor(() => expect(mocks.enable).toHaveBeenCalledWith("target"));
  });

  it("does not offer account actions when the returned identity differs", async () => {
    mocks.getConnection.mockResolvedValue({ data: { connection_id: "other", provider: "notion", auth_mode: "oauth_user" } });
    renderPage("/cloud-documents?provider=notion&connection_id=target");
    await screen.findByText("modelProvider.cloudDocuments.recoveryUnavailable");
    expect(screen.queryByText("modelProvider.cloudDocuments.recoveryAuthorize")).toBeNull();
    expect(screen.queryByText("modelProvider.cloudDocuments.recoveryEnable")).toBeNull();
    expect(mocks.authorize).not.toHaveBeenCalled();
  });

  it("shows the first-entry guide with locked capabilities before connection", async () => {
    renderPage();

    expect(await screen.findByText("现在进行")).toBeInTheDocument();
    expect(screen.getByText("认证后可用")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "开始第 1 步：去认证" }),
    ).toBeEnabled();
  });

  it.each([
    ["localSourceCount", 1],
    ["isFeishuAuthValid", true],
    ["isNotionAuthValid", true],
    ["isGoogleDriveAuthValid", true],
    ["isMailAuthValid", true],
  ])("does not auto-open after %s is connected but allows manual opening", async (key, value) => {
    mocks.vm[key as string] = value;
    renderPage();

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "新手指引" }));
    expect(await screen.findByText("已完成")).toBeInTheDocument();
    expect(
      within(screen.getByRole("dialog")).getByRole("link", {
        name: "在对话中引用云文档",
      }),
    ).toHaveAttribute("href", "/agent/chat/home");
  });

  it.each(["isGitHubAuthValid", "isGoogleDriveAuthValid"])(
    "keeps knowledge sync unavailable for a chat-only provider (%s)",
    async (providerFlag) => {
      mocks.vm[providerFlag] = true;
      renderPage();

      fireEvent.click(screen.getByRole("button", { name: "新手指引" }));
      const dialog = await screen.findByRole("dialog");
      expect(
        within(dialog).getByRole("button", {
          name: "知识库同步（暂不支持）",
        }),
      ).toBeDisabled();
      expect(
        within(dialog).getByRole("link", { name: "在对话中引用云文档" }),
      ).toHaveAttribute("href", "/agent/chat/home");
    },
  );
  it("waits for connection loading before deciding whether to auto-open", async () => {
    mocks.vm.loading = true;
    const view = renderPage();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    mocks.vm.loading = false;
    mocks.vm.isFeishuAuthValid = true;
    view.rerender(<MemoryRouter><CloudDocumentsPage /></MemoryRouter>);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "新手指引" }));
    expect(await screen.findByText("已解锁")).toBeInTheDocument();
  });


  it("keeps a header entry that reopens the guide", async () => {
    window.localStorage.setItem(
      "lazymind.cloud-documents.onboarding.v2",
      "seen",
    );
    renderPage();

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("button", { name: "新手指引" }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("counts mailbox alongside the configured providers and local files", async () => {
    window.localStorage.setItem(
      "lazymind.cloud-documents.onboarding.v2",
      "seen",
    );
    const { unmount } = renderPage();
    expect(await screen.findByText("0 / 6")).toBeInTheDocument();
    unmount();
    mocks.vm.isMailAuthValid = true;
    renderPage();
    expect(await screen.findByText("1 / 6")).toBeInTheDocument();
  });

  it("opens the selected provider setup from the source-choice stage", async () => {
    renderPage();

    fireEvent.click(
      await screen.findByRole("button", { name: "开始第 1 步：去认证" }),
    );
    fireEvent.click(screen.getByRole("button", { name: /Notion/ }));

    await waitFor(() => {
      expect(mocks.vm.handleOpenNotionSetup).toHaveBeenCalledTimes(1);
    });
  });

  it("opens GitHub OAuth setup from the source-choice stage", async () => {
    renderPage();

    fireEvent.click(
      await screen.findByRole("button", { name: "开始第 1 步：去认证" }),
    );
    fireEvent.click(screen.getByRole("button", { name: /GitHub/ }));

    await waitFor(() => {
      expect(mocks.vm.handleOpenGitHubSetup).toHaveBeenCalledTimes(1);
    });
  });

  it("inherits the newly connected provider in the success action", async () => {
    window.sessionStorage.setItem(
      "lazymind.cloud-documents.connection-success.v1",
      "notion",
    );
    mocks.vm.isNotionAuthValid = true;
    renderPage();

    const dialog = await screen.findByRole("dialog", { name: "连接成功" });
    expect(
      within(dialog).getByRole("link", { name: /去创建云文档知识库/ }),
    ).toHaveAttribute(
      "href",
      "/lib/knowledge/list?createSource=cloud-documents&provider=notion",
    );
  });
});
