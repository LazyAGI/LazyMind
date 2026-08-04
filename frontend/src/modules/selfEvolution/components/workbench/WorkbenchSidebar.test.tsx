import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { WorkbenchSidebar } from "./WorkbenchSidebar";
import type { SelfEvolutionChatMessage } from "../types";
import type { SelfEvolutionSessionSummary } from "./types";

function makeSession(overrides: Partial<SelfEvolutionSessionSummary> = {}): SelfEvolutionSessionSummary {
  return { id: "session-1", title: "Session 1", ...overrides };
}

function makeMessage(overrides: Partial<SelfEvolutionChatMessage> = {}): SelfEvolutionChatMessage {
  return { id: "msg-1", role: "user", content: "hello", time: "10:00", ...overrides };
}

const baseProps = {
  activeStepText: "Dataset",
  isRestoringThread: false,
  threadRestoreError: "",
  activeStageLabel: "Dataset stage",
  activeSession: makeSession(),
  chatSessionsCount: 1,
  artifactNavigationPanel: <div>nav-panel</div>,
  isArtifactPanelOpen: false,
  onCloseArtifactPanel: vi.fn(),
  onWorkbenchTabChange: vi.fn(),
  onRetryRestoreThread: vi.fn(),
  onOpenHistorySessionModal: vi.fn(),
  onCloseSession: vi.fn(),
  onCreateSession: vi.fn(),
  onMessageAnchorClick: vi.fn(),
};

describe("WorkbenchSidebar", () => {
  it("renders the current focus text and title", () => {
    renderWithProviders(<WorkbenchSidebar {...baseProps} displayedMessages={[]} />);
    expect(screen.getByText("selfEvolutionRun.executionOrchestration")).toBeInTheDocument();
    expect(
      screen.getByText("selfEvolutionRun.currentFocus"),
    ).toBeInTheDocument();
  });

  it("expands the messages section and calls onWorkbenchTabChange when the toggle is clicked", () => {
    const onWorkbenchTabChange = vi.fn();
    renderWithProviders(
      <WorkbenchSidebar
        {...baseProps}
        displayedMessages={[makeMessage()]}
        activeWorkbenchTab={undefined}
        onWorkbenchTabChange={onWorkbenchTabChange}
      />,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.navInteractionTitle"));
    expect(onWorkbenchTabChange).toHaveBeenCalledWith("messages");
  });

  it("lists user message anchors and calls onMessageAnchorClick when clicked", () => {
    const onMessageAnchorClick = vi.fn();
    const message = makeMessage({ id: "msg-42", content: "what is rag" });
    renderWithProviders(
      <WorkbenchSidebar
        {...baseProps}
        displayedMessages={[message]}
        activeWorkbenchTab="messages"
        onMessageAnchorClick={onMessageAnchorClick}
      />,
    );
    fireEvent.click(screen.getByText("what is rag"));
    expect(onMessageAnchorClick).toHaveBeenCalledWith("msg-42");
  });

  it("shows the no-user-messages hint when there are no user messages", () => {
    renderWithProviders(
      <WorkbenchSidebar
        {...baseProps}
        displayedMessages={[makeMessage({ role: "assistant" })]}
        activeWorkbenchTab="messages"
      />,
    );
    expect(screen.getByText("selfEvolutionRun.noUserMessages")).toBeInTheDocument();
  });

  it("hides the close-session button when there is only one chat session", () => {
    renderWithProviders(
      <WorkbenchSidebar {...baseProps} displayedMessages={[]} chatSessionsCount={1} />,
    );
    expect(screen.queryByTitle("selfEvolutionRun.closeCurrentSession")).not.toBeInTheDocument();
  });

  it("shows and triggers the close-session action when there are multiple sessions", () => {
    const onCloseSession = vi.fn();
    renderWithProviders(
      <WorkbenchSidebar
        {...baseProps}
        displayedMessages={[]}
        chatSessionsCount={2}
        onCloseSession={onCloseSession}
      />,
    );
    fireEvent.click(screen.getByTitle("selfEvolutionRun.closeCurrentSession"));
    expect(onCloseSession).toHaveBeenCalledWith("session-1");
  });

  it("calls onCreateSession and onOpenHistorySessionModal from the action buttons", () => {
    const onCreateSession = vi.fn();
    const onOpenHistorySessionModal = vi.fn();
    renderWithProviders(
      <WorkbenchSidebar
        {...baseProps}
        displayedMessages={[]}
        onCreateSession={onCreateSession}
        onOpenHistorySessionModal={onOpenHistorySessionModal}
      />,
    );
    fireEvent.click(screen.getByTitle("selfEvolutionRun.newSession"));
    fireEvent.click(screen.getByTitle("selfEvolutionRun.openHistoryAria"));
    expect(onCreateSession).toHaveBeenCalledTimes(1);
    expect(onOpenHistorySessionModal).toHaveBeenCalledTimes(1);
  });

  it("shows the retry action for a thread restore error", () => {
    const onRetryRestoreThread = vi.fn();
    renderWithProviders(
      <WorkbenchSidebar
        {...baseProps}
        displayedMessages={[]}
        routeThreadId="thread-1"
        threadRestoreError="failed to restore"
        onRetryRestoreThread={onRetryRestoreThread}
      />,
    );
    expect(screen.getByText("failed to restore")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.retry"));
    expect(onRetryRestoreThread).toHaveBeenCalledTimes(1);
  });
});
