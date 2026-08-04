import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import SkillPackageEditor from "./SkillPackageEditor";
import {
  getSkillTree,
  getSkillDraftStatus,
  compareSkillTreeDiff,
  compareSkillFileDiff,
  probeSkillAgentReviewMode,
  readSkillFsFile,
  writeSkillDraftText,
  commitSkillDraft,
  discardSkillDraft,
} from "../../skillApi";

vi.mock("../../skillApi", async () => {
  const actual = await vi.importActual<typeof import("../../skillApi")>(
    "../../skillApi",
  );
  return {
    ...actual,
    getSkillTree: vi.fn(),
    getSkillDraftStatus: vi.fn(),
    compareSkillTreeDiff: vi.fn(),
    compareSkillFileDiff: vi.fn(),
    probeSkillAgentReviewMode: vi.fn().mockResolvedValue(false),
    readSkillFsFile: vi.fn(),
    writeSkillDraftText: vi.fn(),
    commitSkillDraft: vi.fn(),
    discardSkillDraft: vi.fn(),
    confirmSkillDraft: vi.fn(),
    deleteSkillDraftPath: vi.fn(),
    mkdirSkillDraftPath: vi.fn(),
    uploadSkillDraftFile: vi.fn(),
    submitSkillDraftReviewActions: vi.fn(),
    undoSkillDraftReview: vi.fn(),
    commitSkillDraftReview: vi.fn(),
  };
});

vi.mock("../../skillUpload", () => ({
  uploadSkillTempFile: vi.fn(),
}));

vi.mock("@/modules/knowledge/components/MarkdownViewer", () => ({
  default: ({ children }: { children: string }) => <div data-testid="markdown-viewer">{children}</div>,
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const treeRoot = {
  name: "root",
  path: "",
  type: "dir" as const,
  fileType: "",
  mime: "",
  size: 0,
  binary: false,
  blobHash: "",
  children: [
    {
      name: "SKILL.md",
      path: "SKILL.md",
      type: "file" as const,
      fileType: "markdown",
      mime: "text/markdown",
      size: 10,
      binary: false,
      blobHash: "hash1",
      children: [],
    },
  ],
};

const draftStatus = {
  baseRevisionId: "rev-1",
  conversationId: "",
  draftVersion: 1,
  hasUncommittedDraft: false,
  overlayCount: 0,
  taskId: "",
};

describe("SkillPackageEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (getSkillTree as any).mockResolvedValue(treeRoot);
    (getSkillDraftStatus as any).mockResolvedValue(draftStatus);
    (compareSkillTreeDiff as any).mockResolvedValue({ cacheWritten: true, files: [] });
    (compareSkillFileDiff as any).mockResolvedValue({
      path: "SKILL.md",
      status: "unchanged",
      binary: false,
      type: "file",
      tooLarge: false,
      diffEntryLines: [],
    });
    (probeSkillAgentReviewMode as any).mockResolvedValue(false);
    (readSkillFsFile as any).mockResolvedValue({
      path: "SKILL.md",
      binary: false,
      content: "# Hello skill",
      mime: "text/markdown",
      fileType: "markdown",
      downloadUrl: "",
      blobHash: "hash1",
    });
  });

  it("loads the tree and renders the default markdown file content", async () => {
    render(<SkillPackageEditor skillId="skill-1" canEdit t={t} />);
    await waitFor(() => {
      expect(screen.getByTestId("markdown-viewer")).toHaveTextContent("Hello skill");
    });
    expect(screen.getAllByText("SKILL.md").length).toBeGreaterThan(0);
  });

  it("shows an error alert with a retry button when loading fails", async () => {
    (getSkillTree as any).mockRejectedValueOnce(new Error("boom"));
    render(<SkillPackageEditor skillId="skill-1" canEdit t={t} />);
    await waitFor(() => {
      expect(screen.getByText("common.retry")).toBeInTheDocument();
    });
  });

  it("switches into edit mode and saves the file content", async () => {
    (writeSkillDraftText as any).mockResolvedValue(2);
    render(<SkillPackageEditor skillId="skill-1" canEdit t={t} />);
    await waitFor(() => {
      expect(screen.getByTestId("markdown-viewer")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("common.edit"));
    const textarea = document.querySelector("textarea") as HTMLTextAreaElement;
    expect(textarea).not.toBeNull();
    fireEvent.change(textarea, { target: { value: "# Updated content" } });
    fireEvent.click(screen.getByText("common.save"));

    await waitFor(() => {
      expect(writeSkillDraftText).toHaveBeenCalledWith(
        "skill-1",
        expect.objectContaining({ path: "SKILL.md", content: "# Updated content" }),
      );
    });
  });

  it("shows the uncommitted draft alert and commits the draft", async () => {
    (getSkillDraftStatus as any).mockResolvedValue({
      ...draftStatus,
      hasUncommittedDraft: true,
    });
    (commitSkillDraft as any).mockResolvedValue(undefined);
    render(<SkillPackageEditor skillId="skill-1" canEdit t={t} />);
    await waitFor(() => {
      expect(
        screen.getByText("admin.memorySkillPackageUncommittedTitle"),
      ).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("admin.memorySkillDraftCommit"));
    await waitFor(() => {
      expect(commitSkillDraft).toHaveBeenCalledWith("skill-1", 1);
    });
  });

  it("shows the review alert and hides create-file actions when in review mode", async () => {
    (getSkillDraftStatus as any).mockResolvedValue({
      ...draftStatus,
      hasUncommittedDraft: true,
      taskId: "task-1",
      conversationId: "conv-1",
    });
    (probeSkillAgentReviewMode as any).mockResolvedValue(true);
    render(<SkillPackageEditor skillId="skill-1" canEdit t={t} />);
    await waitFor(() => {
      expect(
        screen.getByText("admin.memorySkillPackageReviewTitle"),
      ).toBeInTheDocument();
    });
    expect(screen.queryByText("admin.memorySkillPackageNewFile")).not.toBeInTheDocument();
  });

  it("does not render edit/upload actions when canEdit is false", async () => {
    render(<SkillPackageEditor skillId="skill-1" canEdit={false} t={t} />);
    await waitFor(() => {
      expect(screen.getByTestId("markdown-viewer")).toBeInTheDocument();
    });
    expect(screen.queryByText("common.edit")).not.toBeInTheDocument();
    expect(screen.queryByText("admin.memorySkillPackageNewFile")).not.toBeInTheDocument();
  });
});
