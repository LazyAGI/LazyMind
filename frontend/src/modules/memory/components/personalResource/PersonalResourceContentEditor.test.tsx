import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import PersonalResourceContentEditor from "./PersonalResourceContentEditor";

const t = (key: string) => key;

const previewManagedPreferenceDraft = vi.fn();
const readPersonalResourceFile = vi.fn();
const writePersonalResourceDraft = vi.fn();
const commitPersonalResourceDraft = vi.fn();
const confirmManagedPreferenceDraft = vi.fn();
const discardPersonalResourceDraft = vi.fn();
const reviewManagedPreferenceDraftHunks = vi.fn();
const undoManagedPreferenceDraftReview = vi.fn();

vi.mock("../../preferenceApi", () => ({
  previewManagedPreferenceDraft: (...args: unknown[]) =>
    previewManagedPreferenceDraft(...args),
  readPersonalResourceFile: (...args: unknown[]) => readPersonalResourceFile(...args),
  writePersonalResourceDraft: (...args: unknown[]) => writePersonalResourceDraft(...args),
  commitPersonalResourceDraft: (...args: unknown[]) => commitPersonalResourceDraft(...args),
  confirmManagedPreferenceDraft: (...args: unknown[]) => confirmManagedPreferenceDraft(...args),
  discardPersonalResourceDraft: (...args: unknown[]) => discardPersonalResourceDraft(...args),
  reviewManagedPreferenceDraftHunks: (...args: unknown[]) =>
    reviewManagedPreferenceDraftHunks(...args),
  undoManagedPreferenceDraftReview: (...args: unknown[]) =>
    undoManagedPreferenceDraftReview(...args),
  hasPersonalResourceDraftChanges: ({
    draftStatus,
    headContent,
    draftContent,
  }: { draftStatus?: string; headContent: string; draftContent: string }) => {
    const normalizedStatus = (draftStatus || "").trim().toLowerCase();
    if (normalizedStatus && normalizedStatus !== "none") {
      return true;
    }
    return draftContent.trim() !== headContent.trim();
  },
  resolveManagedPreferenceDraftKind: (resourceType?: string) =>
    resourceType?.includes("memory") ? "memory" : "user-preference",
  resolvePersonalResourceApiType: (resourceType?: string) =>
    resourceType?.includes("memory") ? "memory" : "user_preference",
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: unknown) => String((error as Error)?.message || error),
}));

vi.mock("@/modules/knowledge/components/MarkdownViewer", () => ({
  default: ({ children }: { children: string }) => <div data-testid="markdown-viewer">{children}</div>,
}));

const defaultPreview = {
  currentContent: "head content",
  diff: "",
  draftContent: "head content",
  draftSourceVersion: 0,
  draftStatus: "none",
  draftVersion: 1,
  fileDiff: undefined,
  pendingCount: 0,
  acceptedCount: 0,
  rejectedCount: 0,
};

const defaultHeadFile = {
  content: "head content",
  draftVersion: 1,
  draftStatus: "none",
  revisionId: "rev-1",
  revisionNo: 1,
  binary: false,
};

describe("PersonalResourceContentEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    previewManagedPreferenceDraft.mockResolvedValue(defaultPreview);
    readPersonalResourceFile.mockResolvedValue(defaultHeadFile);
  });

  it("shows a loading spinner while content loads, then renders markdown content", async () => {
    render(<PersonalResourceContentEditor canEdit t={t} />);
    await waitFor(() => {
      expect(screen.getByTestId("markdown-viewer")).toHaveTextContent("head content");
    });
  });

  it("shows an error alert with retry when loading fails", async () => {
    readPersonalResourceFile.mockRejectedValueOnce(new Error("load failed"));
    render(<PersonalResourceContentEditor canEdit t={t} />);
    await waitFor(() => {
      expect(screen.getByText("load failed")).toBeInTheDocument();
    });
    expect(screen.getByText("common.retry")).toBeInTheDocument();
  });

  it("enters edit mode and saves a draft", async () => {
    writePersonalResourceDraft.mockResolvedValue(2);
    previewManagedPreferenceDraft
      .mockResolvedValueOnce(defaultPreview)
      .mockResolvedValueOnce({
        ...defaultPreview,
        draftContent: "edited content",
        draftStatus: "draft",
      });

    render(<PersonalResourceContentEditor canEdit t={t} />);
    await waitFor(() => {
      expect(screen.getByTestId("markdown-viewer")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("common.edit"));
    const textarea = screen.getByRole("textbox");
    fireEvent.change(textarea, { target: { value: "edited content" } });
    fireEvent.click(screen.getByText("common.save"));

    await waitFor(() => {
      expect(writePersonalResourceDraft).toHaveBeenCalledWith(
        "user_preference",
        expect.objectContaining({ content: "edited content", expectedDraftVersion: 1 }),
      );
    });
  });

  it("does not render edit controls when canEdit is false", async () => {
    render(<PersonalResourceContentEditor canEdit={false} t={t} />);
    await waitFor(() => {
      expect(screen.getByTestId("markdown-viewer")).toBeInTheDocument();
    });
    expect(screen.queryByText("common.edit")).not.toBeInTheDocument();
  });

  it("renders an empty placeholder when there is no content", async () => {
    previewManagedPreferenceDraft.mockResolvedValue({
      ...defaultPreview,
      currentContent: "",
      draftContent: "",
    });
    readPersonalResourceFile.mockResolvedValue({ ...defaultHeadFile, content: "" });

    render(<PersonalResourceContentEditor canEdit t={t} />);
    await waitFor(() => {
      expect(screen.getByText("-")).toBeInTheDocument();
    });
  });
});
