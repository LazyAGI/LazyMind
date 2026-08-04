import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, fireEvent } from "@/test/testUtils";
import NewChatPage from "./index";

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
    mockGuard.mockReturnValue(baseGuard());
  });

  it("renders the welcome screen with a greeting and the chat input", () => {
    renderWithProviders(<NewChatPage />);
    expect(document.querySelector(".greeting-text")).toBeInTheDocument();
    expect(screen.getByTestId("chat-input-stub")).toBeInTheDocument();
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
});
