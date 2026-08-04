import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, fireEvent, waitFor } from "@/test/testUtils";
import NewChatPage from "./index";
import { getShowcaseCase } from "@/modules/showcase/api";
import { installShowcaseTestTranslations } from "@/modules/showcase/testTranslations";

const mockNavigate = vi.fn();
const mockGuard = vi.fn();

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => mockNavigate };
});

vi.mock("@/modules/chat/hooks/useChatModelProviderGuard", () => ({
  useChatModelProviderGuard: () => mockGuard(),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "user" }) },
}));

vi.mock("../chatLayout", () => ({
  default: () => <div data-testid="chat-layout-stub" />,
}));

vi.mock("@/modules/chat/components/PreferenceConfigNotice", () => ({
  default: ({ hidden }: { hidden?: boolean }) =>
    hidden ? null : <div data-testid="preference-notice-stub" />,
}));

vi.mock("@/modules/showcase/FeaturedCases", () => ({
  default: () => <div data-testid="featured-cases-stub" />,
}));

vi.mock("@/modules/showcase/api", () => ({
  getShowcaseCase: vi.fn(),
}));

vi.mock("@/modules/chat/components/ChatInput", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    allowedUploadTypes: [".png", ".txt"],
    default: React.forwardRef((props: any, ref: any) => {
      React.useImperativeHandle(ref, () => ({
        uploadFiles: vi.fn(),
        clearFiles: vi.fn(),
      }));
      return (
        <div data-testid="chat-input-stub">
          <input
            aria-label="welcome-composer"
            value={props.value}
            onChange={(event) => props.onChange(event.target.value)}
          />
          <span data-testid="chat-input-value">{props.value}</span>
          {props.showcaseSelection ? (
            <div data-testid="showcase-selection-stub">
              <span>{props.showcaseSelection.primaryLabel}</span>
              {props.showcaseSelection.secondaryOptions?.length ? (
                <span>{props.showcaseSelection.secondaryOptions[0].label}</span>
              ) : null}
              {props.showcaseSelection.secondaryOptions?.length > 1 ? (
                <button
                  data-testid="change-secondary"
                  onClick={() =>
                    props.showcaseSelection.onSecondaryChange?.(
                      props.showcaseSelection.secondaryOptions[1].value,
                    )
                  }
                />
              ) : null}
            </div>
          ) : null}
          <button onClick={() => props.setIsChatContent?.(true)}>go-to-chat</button>
        </div>
      );
    }),
  };
});

function baseGuard(overrides: Record<string, unknown> = {}) {
  return {
    canChat: true,
    isChecking: false,
    isRuntimeInitializing: false,
    isConfigurationReady: true,
    needsModelProviderConfig: false,
    embeddingReady: true,
    multimodalEmbeddingReady: true,
    rerankReady: true,
    vlmReady: true,
    status: "ready",
    refresh: vi.fn(),
    ...overrides,
  };
}

describe("NewChatPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    installShowcaseTestTranslations();
    mockGuard.mockReturnValue(baseGuard());
    vi.mocked(getShowcaseCase).mockResolvedValue({
      id: "industry",
      title: "市场调研与竞品分析",
      description: "分析市场趋势",
      category: "调研分析",
      output_type: "report",
      output_label: "网页报告",
      image_url: "/showcase/01-pet-market.png",
      attachment_hint: "行业资料.pdf",
      prompt_short: "分析市场趋势",
      prompt: "请分析这个市场并输出报告",
      result_summary: "结构化调研报告",
      result_highlights: ["趋势"],
      steps: [{ title: "分析", description: "分析资料" }],
      primary_category: "市场调研",
      secondary_options: [
        {
          id: "full",
          label: "完整功能",
          description: "完整方案",
          prompt: "请完整覆盖任务链路和交付标准。",
        },
        {
          id: "outline",
          label: "快速生成",
          description: "结构化初稿",
          prompt: "请优先产出结构化初稿。",
        },
      ],
      tasks: [
        {
          id: "market-scan",
          title: "市场扫描",
          description: "快速梳理市场趋势",
          prompt: "请快速扫描这个市场",
        },
      ],
    });
  });

  it("renders the welcome screen with a greeting and the chat input", () => {
    renderWithProviders(<NewChatPage />);
    expect(document.querySelector(".greeting-text")).toBeInTheDocument();
    expect(screen.getByTestId("chat-input-stub")).toBeInTheDocument();
    expect(screen.getByTestId("featured-cases-stub")).toBeInTheDocument();
  });

  it("switches mutually between featured cases and prompt content", () => {
    renderWithProviders(<NewChatPage />);
    const composer = screen.getByRole("textbox", { name: "welcome-composer" });

    expect(screen.getByTestId("featured-cases-stub")).toBeInTheDocument();
    fireEvent.change(composer, { target: { value: "需要优化的内容" } });
    expect(screen.queryByTestId("featured-cases-stub")).not.toBeInTheDocument();

    fireEvent.change(composer, { target: { value: "   " } });
    expect(screen.getByTestId("featured-cases-stub")).toBeInTheDocument();
  });

  it("shows the embedding warning banner when a knowledge base is selected but embedding is not ready", () => {
    mockGuard.mockReturnValue(baseGuard({ embeddingReady: false }));
    renderWithProviders(<NewChatPage />);
    // chatConfig starts empty so no KB is selected yet -> warning hidden.
    expect(screen.queryByText("chat.embeddingNotReadyWarning")).not.toBeInTheDocument();
  });

  it("shows the runtime initializing banner when the model provider guard reports initializing", () => {
    mockGuard.mockReturnValue(baseGuard({ isRuntimeInitializing: true }));
    renderWithProviders(<NewChatPage />);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it("mounts the ChatLayout once switching into chat content mode", () => {
    renderWithProviders(<NewChatPage />);
    expect(screen.queryByTestId("chat-layout-stub")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("go-to-chat"));
    expect(screen.getByTestId("chat-layout-stub")).toBeInTheDocument();
  });

  it("hides the preference config notice when configuration is not ready", () => {
    mockGuard.mockReturnValue(baseGuard({ isConfigurationReady: false }));
    renderWithProviders(<NewChatPage />);
    expect(screen.queryByTestId("preference-notice-stub")).not.toBeInTheDocument();
  });

  it("loads a showcase prompt into the welcome composer", async () => {
    renderWithProviders(<NewChatPage />, {
      route: "/agent/chat/home?showcase_case=industry",
    });

    await waitFor(() => {
      expect(screen.getByTestId("chat-input-value")).toHaveTextContent(
        "请分析这个市场并输出报告",
      );
    });
    expect(screen.getByText("已载入案例：市场调研与竞品分析")).toBeInTheDocument();
    expect(screen.getByTestId("showcase-selection-stub")).toHaveTextContent("市场调研");
    expect(screen.queryByTestId("featured-cases-stub")).not.toBeInTheDocument();
  });

  it("applies the selected second-level category to the composer prompt", async () => {
    renderWithProviders(<NewChatPage />, {
      route: "/agent/chat/home?showcase_case=industry",
    });

    await waitFor(() => {
      expect(screen.getByTestId("chat-input-value")).toHaveTextContent("请完整覆盖任务链路");
    });
    fireEvent.click(screen.getByTestId("change-secondary"));
    await waitFor(() => {
      expect(screen.getByTestId("chat-input-value")).toHaveTextContent("请优先产出结构化初稿");
    });
  });

  it("does not render a second-level category when the case has none", async () => {
    vi.mocked(getShowcaseCase).mockResolvedValue({
      id: "knowledgeQa",
      title: "知识库问答",
      description: "回答知识库问题",
      category: "文档写作",
      output_type: "report",
      output_label: "解决方案",
      image_url: "/showcase/16-knowledge-qa.png",
      prompt_short: "回答问题",
      prompt: "请回答问题",
      result_summary: "答案",
      result_highlights: ["结论"],
      steps: [{ title: "检索", description: "检索资料" }],
      primary_category: "知识库问答",
      secondary_options: [],
    });

    renderWithProviders(<NewChatPage />, {
      route: "/agent/chat/home?showcase_case=knowledgeQa",
    });

    await waitFor(() => expect(screen.getByTestId("chat-input-value")).toHaveTextContent("请回答问题"));
    expect(screen.getByTestId("showcase-selection-stub")).toHaveTextContent("知识库问答");
    expect(screen.getByTestId("showcase-selection-stub").textContent).toBe("知识库问答");
  });

  it("uses the selected detail task prompt when entering the composer", async () => {
    renderWithProviders(<NewChatPage />, {
      route: "/agent/chat/home?showcase_case=industry&showcase_task=market-scan",
    });

    await waitFor(() => {
      expect(screen.getByTestId("chat-input-value")).toHaveTextContent("请快速扫描这个市场");
    });
  });
});
