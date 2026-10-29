import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  listProviders: vi.fn(),
  listGroups: vi.fn(),
  createGroup: vi.fn(),
  selectProvider: vi.fn(),
  t: (key: string, values?: { name?: string; configured?: number; total?: number }) => {
    const copy: Record<string, string> = {
      "modelProvider.external.parsingCategoryTitle": "文档解析",
      "modelProvider.external.toolsCategoryTitle": "搜索引擎",
      "modelProvider.external.academicCategoryTitle": "学术检索",
      "modelProvider.external.translationCategoryTitle": "翻译服务",
      "modelProvider.external.keylessTranslationTitle": "无需配置",
      "modelProvider.external.keyedTranslationTitle": "需要密钥",
      "modelProvider.external.translationGuideOpenOfficial": "打开官方接入说明",
      "modelProvider.external.enableTranslationSource": "启用此翻译源",
    };
    if (key === "modelProvider.defaults.configuredCount") return `${values?.configured} / ${values?.total}`;
    if (key === "modelProvider.external.configModalTitle") return `配置 ${values?.name}`;
    if (key === "modelProvider.external.translationGuideTitle") return `${values?.name} 简易配置教程`;
    return copy[key] || key;
  },
}));

const providers = [
  { id: "mineru", name: "MinerU", category: "ocr", base_url: "https://mineru.net/", is_configured: false },
  { id: "tavily", name: "Tavily", category: "search", is_configured: false },
  { id: "sciverse", name: "Sciverse", category: "datasource", is_configured: false },
  { id: "google-web", name: "Google Translate Web", category: "translation", capabilities: ["keyless_translation"], is_configured: false },
  { id: "baidu", name: "Baidu Translation", category: "translation", capabilities: [], is_configured: false },
];

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN" },
    t: mocks.t,
  }),
}));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: () => "请求失败",
  localizeErrorCode: (code: string) => code,
}));
vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: mocks.listProviders,
    apiCoreModelProvidersModelProviderIdGroupsGet: mocks.listGroups,
    apiCoreModelProvidersModelProviderIdGroupsPost: mocks.createGroup,
    apiCoreModelProvidersSelectedProvidersGet: async () => ({ data: { selections: [] } }),
    apiCoreModelProvidersSelectedProvidersPut: mocks.selectProvider,
  },
  modelProvidersDefaultApi: {},
  unwrapModelProviderData: (data: unknown) => data,
  withModelProviderJsonOptions: (options: unknown) => options,
}));
vi.mock("../components/ToolManagementSection", () => ({ default: () => null }));
vi.mock("../components/DependencyInstallSection", () => ({ default: () => null }));
vi.mock("@/utils/developerMode", () => ({
  DEVELOPER_ACTIVE_EVENT: "fixture-developer-mode",
  isDeveloperModeActive: () => false,
}));

import ExternalServicesPage from "./ExternalServicesPage";

beforeEach(() => {
  vi.clearAllMocks();
  mocks.listProviders.mockResolvedValue({ data: { providers } });
  mocks.listGroups.mockResolvedValue({ data: { groups: [] } });
  mocks.createGroup.mockResolvedValue({ data: { id: "group-google-web", name: "Google Translate Web" } });
  mocks.selectProvider.mockResolvedValue({ data: {} });
});

describe("default-model service configuration", () => {
  it("keeps the document parsing switch visible when no OCR provider is available", async () => {
    mocks.listProviders.mockResolvedValue({ data: { providers: providers.filter((item) => item.category !== "ocr") } });
    render(<ExternalServicesPage
      focusCategory="parsing"
      parsingControl={<span>文档解析总开关</span>}
      variant="default-models"
    />);

    const parsing = await screen.findByRole("region", { name: "文档解析" });
    expect(within(parsing).getByText("文档解析总开关")).toBeVisible();
  });

  it("shows OCR, search, academic search and two translation setup groups", async () => {
    render(<ExternalServicesPage
      focusCategory="translation"
      parsingControl={<span>文档解析总开关</span>}
      variant="default-models"
    />);

    const translation = await screen.findByRole("region", { name: "翻译服务" });
    expect(translation).toHaveAttribute("id", "default-service-translation");
    expect(translation).toHaveAttribute("data-service-category", "translation");
    expect(within(translation).getByRole("button", { name: /翻译服务/ })).toHaveAttribute("aria-expanded", "true");
    expect(within(translation).getByRole("heading", { name: "无需配置" })).toBeInTheDocument();
    expect(within(translation).getByRole("heading", { name: "需要密钥" })).toBeInTheDocument();
    expect(within(translation).getByRole("button", { name: "配置 谷歌翻译（网页）" })).toBeInTheDocument();
    expect(within(translation).getByRole("button", { name: "配置 百度翻译" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "学术检索" })).toHaveAttribute("id", "default-service-academic");

    fireEvent.click(screen.getByRole("button", { name: /文档解析/ }));
    expect(screen.getByText("文档解析总开关")).toBeVisible();
    expect(within(screen.getByRole("region", { name: "文档解析" })).queryByRole("textbox")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /搜索引擎/ }));
    expect(within(screen.getByRole("region", { name: "搜索引擎" })).queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("shows a three-step official guide for a translation source that requires a key", async () => {
    render(<ExternalServicesPage focusCategory="translation" variant="default-models" />);
    fireEvent.click(await screen.findByRole("button", { name: "配置 百度翻译" }));

    const guide = await screen.findByRole("region", { name: "百度翻译 简易配置教程" });
    expect(within(guide).getAllByRole("listitem")).toHaveLength(3);
    expect(within(guide).getByRole("link", { name: /打开官方接入说明/ }))
      .toHaveAttribute("href", "https://fanyi-api.baidu.com/");
  });

  it("uses recognizable local logos for each visible translation service", async () => {
    mocks.listProviders.mockResolvedValue({ data: { providers: [
      { id: "google-web", name: "Google Translate Web", category: "translation", capabilities: ["keyless_translation"] },
      { id: "bing-web", name: "Bing Translate Web", category: "translation", capabilities: ["keyless_translation"] },
      { id: "sogou-web", name: "Sogou Translate Web", category: "translation", capabilities: ["keyless_translation"] },
      { id: "deepl-web", name: "DeepL Web", category: "translation", capabilities: ["keyless_translation"] },
      { id: "tencent-web", name: "Tencent TranSmart Web", category: "translation", capabilities: ["keyless_translation"] },
      { id: "baidu", name: "Baidu Translation", category: "translation" },
      { id: "aliyun", name: "Aliyun Translation", category: "translation" },
      { id: "niutrans", name: "NiuTrans", category: "translation" },
    ] } });
    render(<ExternalServicesPage focusCategory="translation" variant="default-models" />);
    for (const [name, icon] of [
      ["谷歌翻译（网页）", "google.svg"], ["必应翻译（网页）", "bing.png"],
      ["搜狗翻译（网页）", "sogou.png"], ["DeepL 网页翻译", "deepl.png"],
      ["腾讯翻译君（网页）", "tencent.png"], ["百度翻译", "baidu.png"],
      ["阿里云机器翻译", "aliyun.png"], ["小牛翻译", "niutrans.png"],
    ]) {
      const card = await screen.findByRole("button", { name: `配置 ${name}` });
      expect(card.querySelector("img")?.getAttribute("src")).toContain(icon);
    }
  });

  it("notifies the default-model page after enabling a keyless source", async () => {
    const onConfigurationChanged = vi.fn();
    render(<ExternalServicesPage
      focusCategory="translation"
      onConfigurationChanged={onConfigurationChanged}
      variant="default-models"
    />);
    fireEvent.click(await screen.findByRole("button", { name: "配置 谷歌翻译（网页）" }));
    fireEvent.click(await screen.findByRole("button", { name: "启用此翻译源" }));

    await waitFor(() => expect(mocks.createGroup).toHaveBeenCalled());
    expect(mocks.selectProvider).not.toHaveBeenCalled();
    expect(onConfigurationChanged).toHaveBeenCalledTimes(1);
  });

  it("does not offer source selection beside saved translation credentials", async () => {
    mocks.listGroups.mockResolvedValue({ data: { groups: [{
      id: "group-baidu",
      has_api_key: true,
      keys: [{ id: "key-baidu", masked: "****1234" }],
    }] } });
    const onConfigurationChanged = vi.fn();
    render(<ExternalServicesPage
      focusCategory="translation"
      onConfigurationChanged={onConfigurationChanged}
      variant="default-models"
    />);
    fireEvent.click(await screen.findByRole("button", { name: "配置 百度翻译" }));
    expect(screen.queryByRole("button", { name: "modelProvider.external.selectTranslationSource" })).not.toBeInTheDocument();
    expect(mocks.selectProvider).not.toHaveBeenCalled();
    expect(onConfigurationChanged).not.toHaveBeenCalled();
  });
});
