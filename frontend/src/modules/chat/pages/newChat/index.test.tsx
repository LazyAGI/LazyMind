import { fireEvent, render, screen } from "@testing-library/react";
import { forwardRef, useImperativeHandle } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import NewChatPage from "./index";
import type { ShowcaseCase } from "@/modules/showcase/api";

const mocks = vi.hoisted(() => ({
  setThinkingDepth: vi.fn(),
  getShowcaseCase: vi.fn(),
  getKnowledgeMarketItem: vi.fn(),
  clearFiles: vi.fn(),
}));

const featuredCase: ShowcaseCase = {
  builtin_skill_uid: "builtin.product-design",
  id: "aiProduct",
  category: "product",
  description: "从需求生成产品方案",
  detail_description: "产品设计详情",
  detail_title: "产品设计",
  featured: true,
  featured_order: 1,
  gallery: true,
  image_url: "/showcase/product.png",
  output_label: "PRD",
  output_type: "document",
  prompt: "帮我生成一份产品方案",
  prompt_short: "生成产品方案",
  result_summary: "产品需求文档",
  title: "产品设计与 PRD 生成",
  type: "chat",
};

vi.mock("react-i18next", async (importOriginal) => {
  const original = await importOriginal<typeof import("react-i18next")>();
  return {
    ...original,
    useTranslation: () => ({
      i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
      t: (key: string) => key,
    }),
  };
});

vi.mock("@/modules/chat/components/ChatInput", () => ({
  default: forwardRef(function MockChatInput(props: any, ref) {
    useImperativeHandle(ref, () => ({
      clearFiles: mocks.clearFiles,
      focus: vi.fn(),
      element: null,
    }));
    return (
      <div>
        <textarea
          aria-label="chat-input"
          value={props.value}
          onChange={(event) => props.onChange(event.target.value)}
        />
        {props.showPromptSuggestions !== false && props.value.trim() ? (
          <div>prompt-suggestions</div>
        ) : null}
      </div>
    );
  }),
}));

vi.mock("@/modules/showcase/FeaturedCases", () => ({
  default: ({ onTry }: { onTry: (item: ShowcaseCase) => void }) => (
    <section aria-label="featured-cases">
      <button type="button" onClick={() => onTry(featuredCase)}>
        试一试模板
      </button>
    </section>
  ),
}));

vi.mock("../chatLayout", () => ({ default: () => null }));
vi.mock("@/modules/chat/components/PreferenceConfigNotice", () => ({
  default: () => null,
}));
vi.mock("@/modules/chat/hooks/useChatModelProviderGuard", () => ({
  useChatModelProviderGuard: () => ({
    canChat: true,
    embeddingReady: true,
    multimodalEmbeddingReady: true,
    rerankReady: true,
    vlmReady: true,
    needsModelProviderConfig: false,
    status: "ready",
    isRuntimeInitializing: false,
    isChecking: false,
    isConfigurationReady: true,
    refresh: vi.fn(),
  }),
}));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "system-admin" }) },
}));
vi.mock("@/components/request", () => ({
  axiosInstance: {},
  localizeErrorCode: (code: string) => code,
}));
vi.mock("@/modules/chat/store/chatThink", () => ({
  useChatThinkStore: {
    getState: () => ({ setThinkingDepth: mocks.setThinkingDepth }),
  },
}));
vi.mock("@/modules/showcase/api", () => ({
  getShowcaseCase: mocks.getShowcaseCase,
}));
vi.mock("@/modules/showcase/useFeaturedSkillBinding", () => ({
  useFeaturedSkillBinding: () => ({
    mentions: [],
    retry: vi.fn(),
    status: "ready",
  }),
}));
vi.mock("@/modules/knowledge/api/knowledgeMarket", () => ({
  getKnowledgeMarketItem: mocks.getKnowledgeMarketItem,
}));

describe("NewChatPage featured templates", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    mocks.setThinkingDepth.mockClear();
    mocks.getShowcaseCase.mockReset();
    mocks.getKnowledgeMarketItem.mockReset();
    mocks.clearFiles.mockReset();
  });

  it("keeps template controls and capability cards while the user edits the template", () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "试一试模板" }));

    expect(screen.getByRole("textbox", { name: "chat-input" })).toHaveValue(
      featuredCase.prompt,
    );
    expect(screen.getByRole("region", { name: "featured-cases" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "showcase.clearCase" })).toBeEnabled();
    expect(screen.queryByText("prompt-suggestions")).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: "chat-input" }), {
      target: { value: `${featuredCase.prompt}，补充用户要求` },
    });

    expect(screen.getByRole("region", { name: "featured-cases" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "showcase.clearCase" })).toBeEnabled();
    expect(screen.queryByText("prompt-suggestions")).not.toBeInTheDocument();
  });

  it("clears an inserted template and returns to the empty welcome state", () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "试一试模板" }));
    mocks.clearFiles.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "showcase.clearCase" }));

    expect(screen.getByRole("textbox", { name: "chat-input" })).toHaveValue("");
    expect(screen.getByRole("region", { name: "featured-cases" })).toBeInTheDocument();
    expect(screen.queryByText("prompt-suggestions")).not.toBeInTheDocument();
    expect(mocks.clearFiles).toHaveBeenCalledOnce();
  });
});
