import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import ArtifactDownloadButton from "./ArtifactDownloadButton";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
  initReactI18next: { type: "3rdParty", init: () => {} },
}));

vi.mock("./index", () => ({
  default: () => <div data-testid="artifact-collector-stub" />,
}));

describe("ArtifactDownloadButton", () => {
  beforeEach(() => {
    useTaskCenterStore.setState({ artifactsByConversation: {} } as any);
  });

  it("renders nothing when there are no artifacts for the conversation", () => {
    const { container } = render(
      <ArtifactDownloadButton sessionId="s1" historyId="h1" />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when historyId is missing even with artifacts present", () => {
    useTaskCenterStore.setState({
      artifactsByConversation: { s1: [{ artifact_id: "a1" } as any] },
    } as any);
    const { container } = render(<ArtifactDownloadButton sessionId="s1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the download trigger when artifacts exist for the conversation", () => {
    useTaskCenterStore.setState({
      artifactsByConversation: { s1: [{ artifact_id: "a1" } as any] },
    } as any);
    render(<ArtifactDownloadButton sessionId="s1" historyId="h1" />);
    expect(document.querySelector(".artifact-download-trigger")).toBeInTheDocument();
  });

  it("opens the popover with the artifact collector card when clicked", () => {
    useTaskCenterStore.setState({
      artifactsByConversation: { s1: [{ artifact_id: "a1" } as any] },
    } as any);
    render(<ArtifactDownloadButton sessionId="s1" historyId="h1" />);

    fireEvent.click(screen.getByRole("button"));

    expect(screen.getByTestId("artifact-collector-stub")).toBeInTheDocument();
  });
});
