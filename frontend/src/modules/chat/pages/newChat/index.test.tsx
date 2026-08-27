import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { forwardRef, useImperativeHandle } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import NewChatPage from "./index";
import type { ShowcaseCase } from "@/modules/showcase/api";

const mocks = vi.hoisted(() => ({
  setThinkingDepth: vi.fn(),
  listShowcaseCases: vi.fn(),
  getKnowledgeMarketItem: vi.fn(),
  getChatSettings: vi.fn(),
  clearFiles: vi.fn(),
  latestChatInputProps: null as any,
}));

const entryDefaults = {
  quick_question: {
    thinking_depth: "low",
    conversation_settings: {
      chat_executor: "lazymind",
      enable_workflow: false,
      workflow_mode: "auto",
      enable_subagent: false,
    },
  },
  new_task: {
    thinking_depth: "max",
    conversation_settings: {
      chat_executor: "codex",
      enable_workflow: true,
      workflow_mode: "dynamic",
      enable_subagent: true,
    },
  },
};

const featuredCase: ShowcaseCase = {
  provider: "SkillHub",
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
  result_summary: "产品需求文档",
  source_url: "https://skillhub.example/product-design",
  tasks: [
    {
      id: "product-plan",
      title: "产品方案",
      description: "生成产品方案",
      output_label: "产品方案",
      prompt: "帮我生成一份产品方案",
      prompt_short: "生成产品方案",
      steps: [],
      result: {
        template: "generic_report_v1",
        eyebrow: "产品方案",
        title: "产品方案",
        summary: "产品方案摘要",
      },
    },
    {
      id: "product-review",
      title: "产品评审",
      description: "评审产品方案",
      output_label: "评审意见",
      prompt: "帮我评审这份产品方案",
      prompt_short: "评审产品方案",
      steps: [],
      result: {
        template: "generic_report_v1",
        eyebrow: "产品评审",
        title: "产品评审",
        summary: "产品评审摘要",
      },
    },
  ],
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
    mocks.latestChatInputProps = props;
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
vi.mock("@/modules/chat/utils/request", () => ({
  FALLBACK_CHAT_ENTRY_DEFAULTS: {
    quick_question: {
      thinking_depth: "medium",
      conversation_settings: {
        chat_executor: "lazymind",
        enable_workflow: false,
        workflow_mode: "dynamic",
        enable_subagent: true,
      },
    },
    new_task: {
      thinking_depth: "high",
      conversation_settings: {
        chat_executor: "lazymind",
        enable_workflow: true,
        workflow_mode: "dynamic",
        enable_subagent: true,
      },
    },
  },
  parseChatEntryDefaults: (payload: any) => payload?.data ?? payload,
  ConversationSettingsApi: () => ({ getChatSettings: mocks.getChatSettings }),
}));
vi.mock("@/modules/chat/store/chatThink", () => ({
  useChatThinkStore: {
    getState: () => ({ setThinkingDepth: mocks.setThinkingDepth }),
  },
}));
vi.mock("@/modules/showcase/api", () => ({
  listShowcaseCases: mocks.listShowcaseCases,
  matchesShowcaseEntryType: (capabilityType: string, entryType: string) =>
    entryType === "chat"
      ? capabilityType === "chat"
      : capabilityType === "work" || capabilityType === "workflow",
}));
vi.mock("@/modules/showcase/useFeaturedCapabilityBinding", () => ({
  useFeaturedCapabilityBinding: () => ({
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
    mocks.listShowcaseCases.mockReset();
    mocks.listShowcaseCases.mockResolvedValue({
      cases: [featuredCase],
      categories: [],
      total: 1,
    });
    mocks.getKnowledgeMarketItem.mockReset();
    mocks.getChatSettings.mockReset();
    mocks.getChatSettings.mockResolvedValue({ data: { data: entryDefaults } });
    mocks.clearFiles.mockReset();
    mocks.latestChatInputProps = null;
  });

  it("applies the configured Quick Q&A defaults to the welcome composer", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("low");
      expect(mocks.latestChatInputProps.initialConversationSettings).toEqual(
        entryDefaults.quick_question.conversation_settings,
      );
    });
  });

  it("applies the configured New task defaults and switches profiles without mixing values", async () => {
    window.sessionStorage.setItem("chat_new_run_in_background", "1");
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("max");
      expect(mocks.latestChatInputProps.initialConversationSettings).toEqual(
        entryDefaults.new_task.conversation_settings,
      );
      expect(mocks.latestChatInputProps.runInBackground).toBe(true);
    });

    act(() => {
      window.dispatchEvent(new CustomEvent("lazymind:chat-select-conversation", {
        detail: { conversationId: "", runInBackground: false },
      }));
    });

    await waitFor(() => {
      expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("low");
      expect(mocks.latestChatInputProps.initialConversationSettings).toEqual(
        entryDefaults.quick_question.conversation_settings,
      );
      expect(mocks.latestChatInputProps.runInBackground).toBe(false);
    });
  });

  it("opens a work demo in New task mode even when Quick Q&A was active", async () => {
    window.sessionStorage.setItem("chat_new_run_in_background", "0");
    const workCase: ShowcaseCase = {
      ...featuredCase,
      id: "ppt-workflow",
      type: "workflow",
      tasks: [{
        ...featuredCase.tasks[0],
        prompt: "生成一份演示文稿",
      }],
    };
    mocks.listShowcaseCases.mockResolvedValue({
      cases: [workCase],
      categories: [],
      total: 1,
    });

    render(
      <MemoryRouter initialEntries={[
        "/agent/chat/home?showcase_case=ppt-workflow&showcase_entry=work",
      ]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByRole("textbox", { name: "chat-input" })).toHaveValue(
        "生成一份演示文稿",
      );
      expect(mocks.latestChatInputProps.runInBackground).toBe(true);
      expect(mocks.latestChatInputProps.initialConversationSettings).toEqual(
        entryDefaults.new_task.conversation_settings,
      );
    });
    expect(window.sessionStorage.getItem("chat_new_run_in_background")).toBe("1");
  });

  it("opens a chat demo in Quick Q&A mode even when New task was active", async () => {
    window.sessionStorage.setItem("chat_new_run_in_background", "1");

    render(
      <MemoryRouter initialEntries={[
        "/agent/chat/home?showcase_case=aiProduct&showcase_task=product-plan&showcase_entry=chat",
      ]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByRole("textbox", { name: "chat-input" })).toHaveValue(
        featuredCase.tasks[0].prompt,
      );
      expect(mocks.latestChatInputProps.runInBackground).toBe(false);
      expect(mocks.latestChatInputProps.initialConversationSettings).toEqual(
        entryDefaults.quick_question.conversation_settings,
      );
    });
    expect(window.sessionStorage.getItem("chat_new_run_in_background")).toBe("0");
  });

  it("derives the correct mode from the case type for legacy links", async () => {
    window.sessionStorage.setItem("chat_new_run_in_background", "0");
    const legacyWorkflow: ShowcaseCase = {
      ...featuredCase,
      id: "legacy-workflow",
      type: "workflow",
      tasks: [featuredCase.tasks[0]],
    };
    mocks.listShowcaseCases.mockResolvedValue({
      cases: [legacyWorkflow],
      categories: [],
      total: 1,
    });

    render(
      <MemoryRouter initialEntries={[
        "/agent/chat/home?showcase_case=legacy-workflow",
      ]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(mocks.latestChatInputProps.runInBackground).toBe(true);
      expect(window.sessionStorage.getItem("chat_new_run_in_background")).toBe("1");
    });
  });

  it("blocks new conversations until failed defaults can be reloaded", async () => {
    mocks.getChatSettings.mockRejectedValueOnce(new Error("offline"));
    window.sessionStorage.setItem("chat_new_run_in_background", "1");

    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("high");
      expect(mocks.latestChatInputProps.initialConversationSettings).toEqual({
        chat_executor: "lazymind",
        enable_workflow: true,
        workflow_mode: "dynamic",
        enable_subagent: true,
      });
    });
    expect(screen.getByText("settingsPage.tasks.entryDefaultsLoadFailed")).toBeInTheDocument();
    expect(mocks.latestChatInputProps.disabled).toBe(true);

    fireEvent.click(screen.getByRole("button", {
      name: "settingsPage.tasks.retryEntryDefaults",
    }));

    await waitFor(() => {
      expect(mocks.latestChatInputProps.disabled).toBe(false);
      expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("max");
      expect(mocks.latestChatInputProps.initialConversationSettings).toEqual(
        entryDefaults.new_task.conversation_settings,
      );
    });
  });

  it("does not apply entry defaults while resuming an existing conversation", async () => {
    window.sessionStorage.setItem("chat_resume_conversation_id", "conversation-1");

    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    expect(mocks.setThinkingDepth).not.toHaveBeenCalled();
    await waitFor(() => expect(mocks.getChatSettings).toHaveBeenCalledOnce());
    await waitFor(() => expect(
      screen.queryByText("settingsPage.tasks.entryDefaultsLoading"),
    ).not.toBeInTheDocument());
    expect(mocks.setThinkingDepth).not.toHaveBeenCalled();
  });

  it("stops applying entry defaults after an existing conversation is selected", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("low");
    });
    mocks.setThinkingDepth.mockClear();

    act(() => {
      window.dispatchEvent(new CustomEvent("lazymind:chat-select-conversation", {
        detail: { conversationId: "conversation-1", source: "sidebar" },
      }));
    });

    await waitFor(() => {
      expect(screen.getByRole("textbox", {
        name: "chat-input",
        hidden: true,
      })).not.toBeVisible();
    });
    expect(mocks.setThinkingDepth).not.toHaveBeenCalled();
  });

  it("keeps the Skill selector visible and reveals functions only after selection", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(mocks.latestChatInputProps.showcaseSelection.skill.options).toEqual([
        {
          value: featuredCase.id,
          label: featuredCase.title,
          description: "productshowcase.capabilitySeparatorPRD",
        },
      ]);
    });
    expect(mocks.latestChatInputProps.showcaseSelection.skill.value).toBeUndefined();
    expect(mocks.latestChatInputProps.showcaseSelection.skill.placeholder).toBe(
      "showcase.chooseSkill",
    );
    expect(mocks.latestChatInputProps.showcaseSelection.task).toBeUndefined();

    act(() => {
      mocks.latestChatInputProps.showcaseSelection.skill.onChange(featuredCase.id);
    });

    await waitFor(() => {
      expect(mocks.latestChatInputProps.showcaseSelection.skill.value).toBe(featuredCase.id);
      expect(mocks.latestChatInputProps.showcaseSelection.task).toBeDefined();
    });

    fireEvent.click(screen.getByRole("button", { name: "showcase.clearCase" }));
    await waitFor(() => {
      expect(mocks.latestChatInputProps.showcaseSelection.skill.value).toBeUndefined();
      expect(mocks.latestChatInputProps.showcaseSelection.task).toBeUndefined();
    });
  });

  it("keeps template controls and capability cards while the user edits the template", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(
      screen.queryByText("settingsPage.tasks.entryDefaultsLoading"),
    ).not.toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "试一试模板" }));

    expect(screen.getByRole("textbox", { name: "chat-input" })).toHaveValue(
      "showcase.fullCapabilityPrompt",
    );
    expect(screen.getByRole("region", { name: "featured-cases" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "showcase.clearCase" })).toBeEnabled();
    expect(screen.queryByText("prompt-suggestions")).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: "chat-input" }), {
      target: { value: "showcase.fullCapabilityPrompt，补充用户要求" },
    });

    expect(screen.getByRole("region", { name: "featured-cases" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "showcase.clearCase" })).toBeEnabled();
    expect(screen.queryByText("prompt-suggestions")).not.toBeInTheDocument();
  });

  it("clears an inserted template and returns to the empty welcome state", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(
      screen.queryByText("settingsPage.tasks.entryDefaultsLoading"),
    ).not.toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "试一试模板" }));
    mocks.clearFiles.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "showcase.clearCase" }));

    expect(screen.getByRole("textbox", { name: "chat-input" })).toHaveValue("");
    expect(screen.getByRole("region", { name: "featured-cases" })).toBeInTheDocument();
    expect(screen.queryByText("prompt-suggestions")).not.toBeInTheDocument();
    expect(mocks.clearFiles).toHaveBeenCalledOnce();
  });

  it("switches between the complete capability and configured functions", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );
    await waitFor(() => expect(mocks.listShowcaseCases).toHaveBeenCalledOnce());

    fireEvent.click(screen.getByRole("button", { name: "试一试模板" }));

    await waitFor(() => {
      expect(mocks.latestChatInputProps.showcaseSelection.task.value).toBe(
        "__full_capability__",
      );
    });
    expect(mocks.latestChatInputProps.showcaseSelection.task.options).toEqual([
      { value: "__full_capability__", label: "showcase.fullCapability" },
      { value: "product-plan", label: "产品方案", description: "生成产品方案" },
      { value: "product-review", label: "产品评审", description: "评审产品方案" },
    ]);
    expect(mocks.latestChatInputProps.showcaseSelection.task.disabled).toBe(false);

    act(() => {
      mocks.latestChatInputProps.showcaseSelection.task.onChange("product-review");
    });
    expect(screen.getByRole("textbox", { name: "chat-input" })).toHaveValue(
      "帮我评审这份产品方案",
    );
  });

  it("shows a fixed complete-capability selector for a single-function Skill", async () => {
    const singleFunctionCase: ShowcaseCase = {
      ...featuredCase,
      id: "single-function",
      tasks: [featuredCase.tasks[0]],
    };
    mocks.listShowcaseCases.mockResolvedValue({
      cases: [singleFunctionCase],
      categories: [],
      total: 1,
    });

    render(
      <MemoryRouter initialEntries={["/agent/chat/home?showcase_case=single-function"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(mocks.latestChatInputProps.showcaseSelection.task.options).toEqual([
        { value: "__full_capability__", label: "showcase.fullCapability" },
      ]);
    });
    expect(mocks.latestChatInputProps.showcaseSelection.task.disabled).toBe(true);
    expect(screen.getByRole("textbox", { name: "chat-input" })).toHaveValue(
      "帮我生成一份产品方案",
    );
  });

  it("limits quick selection to the first five configured capabilities", async () => {
    const cases = Array.from({ length: 6 }, (_, index): ShowcaseCase => ({
      ...featuredCase,
      id: `featured-${index + 1}`,
      title: `精选能力 ${index + 1}`,
      featured_order: index + 1,
    }));
    mocks.listShowcaseCases.mockResolvedValue({ cases, categories: [], total: cases.length });

    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <NewChatPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(mocks.latestChatInputProps.showcaseSelection.skill.options).toHaveLength(5);
    });
    expect(mocks.latestChatInputProps.showcaseSelection.skill.options[4]?.value).toBe(
      "featured-5",
    );
    expect(mocks.latestChatInputProps.showcaseSelection.skill.moreLabel).toBe(
      "showcase.exploreMoreCapabilities",
    );
  });
});
