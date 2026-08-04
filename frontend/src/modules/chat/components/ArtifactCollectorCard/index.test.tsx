import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import ArtifactCollectorCard from "./index";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";

const { mockDownloadStream } = vi.hoisted(() => ({
  mockDownloadStream: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
  initReactI18next: { type: "3rdParty", init: () => {} },
}));

vi.mock("@/modules/chat/utils/download", () => ({
  downloadStream: mockDownloadStream,
}));

vi.mock("@/modules/knowledge/utils/imageUrl", () => ({
  resolveCoreAssetUrl: (path: string) => (path ? `https://cdn.test${path}` : ""),
  basenameFromPath: (path: string) => path.split("/").pop() || path,
}));

function seedArtifacts(sessionId: string) {
  useTaskCenterStore.setState({
    artifactsByConversation: {
      [sessionId]: [
        {
          artifact_id: "a1",
          conversation_id: sessionId,
          history_id: "h1",
          producer_type: "main_agent",
          slot: "report",
          content_type: "text",
          seq: 1,
          value: { text: "hello world" },
        },
        {
          artifact_id: "a2",
          conversation_id: sessionId,
          history_id: "h2",
          producer_type: "main_agent",
          slot: "image",
          content_type: "image",
          seq: 1,
          value: { url: "/uploads/pic.png" },
        },
      ],
    },
    loadConversationArtifacts: vi.fn().mockResolvedValue(undefined),
  } as any);
}

describe("ArtifactCollectorCard", () => {
  beforeEach(() => {
    mockDownloadStream.mockReset();
  });

  it("shows only the files that belong to the current turn by default", async () => {
    seedArtifacts("session-1");
    render(<ArtifactCollectorCard sessionId="session-1" historyId="h1" />);

    expect(await screen.findByText("report.txt")).toBeInTheDocument();
    expect(screen.queryByText("pic.png")).not.toBeInTheDocument();
  });

  it("shows all conversation files when switching to the conversation scope tab", async () => {
    seedArtifacts("session-1");
    render(<ArtifactCollectorCard sessionId="session-1" historyId="h1" />);
    await screen.findByText("report.txt");

    fireEvent.click(screen.getByText(/chat.artifactCollectorConversationTab/));

    expect(await screen.findByText("pic.png")).toBeInTheDocument();
  });

  it("shows the empty state when there are no artifacts for the current turn", async () => {
    seedArtifacts("session-2");
    render(<ArtifactCollectorCard sessionId="session-2" historyId="unknown-history" />);
    expect(
      await screen.findByText("chat.artifactCollectorNoFilesCurrentTurn"),
    ).toBeInTheDocument();
  });

  it("calls onClose when the close button is clicked", async () => {
    seedArtifacts("session-1");
    const onClose = vi.fn();
    render(
      <ArtifactCollectorCard sessionId="session-1" historyId="h1" onClose={onClose} />,
    );
    await screen.findByText("report.txt");
    fireEvent.click(document.querySelector(".artifact-collector__close-btn") as Element);
    expect(onClose).toHaveBeenCalled();
  });

  it("downloads a single selected text artifact directly to disk", async () => {
    seedArtifacts("session-1");
    render(<ArtifactCollectorCard sessionId="session-1" historyId="h1" />);
    await screen.findByText("report.txt");

    fireEvent.click(
      screen.getByText(/chat.artifactCollectorDownloadSelected/),
    );

    await waitFor(() => expect(mockDownloadStream).toHaveBeenCalledTimes(1));
    expect(mockDownloadStream.mock.calls[0][1]).toBe("report.txt");
  });
});
