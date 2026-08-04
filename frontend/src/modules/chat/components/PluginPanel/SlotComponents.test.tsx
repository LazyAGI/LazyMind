import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { SlotRevision } from "@/modules/chat/store/pluginPanel";
import {
  AddSlotItemButton,
  SlotFile,
  SlotImage,
  SlotRenderer,
  SlotText,
} from "./SlotComponents";

const mockDeleteSlotItem = vi.fn();
const mockPatchSlotCaption = vi.fn();
const mockPatchSlotItemValue = vi.fn();
const mockCreateSlotItem = vi.fn();
const mockGetSlotVersions = vi.fn();
const mockRollbackSlotItem = vi.fn();
const mockUploadFileInChunks = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key),
  }),
}));

vi.mock("@/i18n", () => ({
  default: { t: (key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key) },
}));

vi.mock("@/modules/chat/store/pluginPanel", () => ({
  usePluginStore: () => ({
    deleteSlotItem: mockDeleteSlotItem,
    patchSlotCaption: mockPatchSlotCaption,
    patchSlotItemValue: mockPatchSlotItemValue,
    createSlotItem: mockCreateSlotItem,
    getSlotVersions: mockGetSlotVersions,
    rollbackSlotItem: mockRollbackSlotItem,
  }),
  draftStore: {
    getLocalDraft: vi.fn(() => null),
    setDraft: vi.fn(),
    cancelDraft: vi.fn(),
    flushDraft: vi.fn(),
  },
}));

vi.mock("@/modules/knowledge/utils/imageUrl", () => ({
  resolveCoreAssetUrl: (path?: string) => (path ? `resolved:${path}` : ""),
  resolveMarkdownImageUrlAsync: async (path: string) => `resolved:${path}`,
  isExpiredSignedUrl: () => false,
}));

vi.mock("@/modules/memory/shared", () => ({
  buildDiffLinesWithInline: () => [],
}));

vi.mock("@/modules/memory/components/DiffLineContent", () => ({
  DiffLineContent: () => null,
}));

vi.mock("@/modules/chat/utils/chunkUpload", () => ({
  uploadFileInChunks: (...args: unknown[]) => mockUploadFileInChunks(...args),
}));

vi.mock("@/modules/chat/utils/request", () => ({
  PluginSessionApi: () => ({}),
}));

vi.mock("./FilePreviewDrawer", () => ({
  FilePreviewDrawer: ({ open, filename }: { open: boolean; filename: string }) =>
    open ? <div data-testid="file-preview-drawer">{filename}</div> : null,
}));

vi.mock("./writerArtifactViews", () => ({
  WriterArtifactContent: () => <div data-testid="writer-artifact-content" />,
  WRITER_ARTIFACT_SLOT_IDS: new Set(["outline"]),
  unwrapArtifactPayload: (raw: unknown) => raw,
}));

vi.mock("./WriterIRControl", () => ({
  WriterIRControl: () => <div data-testid="writer-ir-control" />,
}));

vi.mock("@/modules/chat/components/MarkdownViewer", () => ({
  default: ({ children }: { children: string }) => <div data-testid="markdown-viewer">{children}</div>,
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => `error:${code}`,
}));

function makeSlot(overrides: Partial<SlotRevision> = {}): SlotRevision {
  return {
    slot_id: "s1",
    revision: 1,
    selected: true,
    slot: "some_slot",
    ...overrides,
  } as SlotRevision;
}

describe("SlotFile", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows a pending placeholder when there is no resolvable file URL", () => {
    render(<SlotFile slot={makeSlot({ artifact_value: undefined })} />);
    expect(screen.getByText("chat.slots.pendingGeneration")).toBeInTheDocument();
  });

  it("renders the file name, size and preview/download actions when the artifact is present", () => {
    render(
      <SlotFile
        slot={makeSlot({
          artifact_value: { url: "/static-files/report.pdf", filename: "report.pdf", size: 2048 },
        })}
      />,
    );
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.getByText("chat.slots.preview")).toBeInTheDocument();
    expect(screen.getByText("chat.slots.download")).toBeInTheDocument();
  });

  it("opens a delete confirmation and calls deleteSlotItem on confirm", async () => {
    render(
      <SlotFile
        slot={makeSlot({ artifact_value: { url: "/f.pdf", filename: "f.pdf" }, list_index: 0 })}
        sessionId="sess-1"
        slotId="slot-1"
      />,
    );
    fireEvent.click(screen.getByLabelText("chat.deleteNamedFile:{\"name\":\"f.pdf\"}"));
    fireEvent.click(screen.getByLabelText("chat.slots.confirmDelete"));
    await waitFor(() => expect(mockDeleteSlotItem).toHaveBeenCalledWith("sess-1", "slot-1", 0));
  });

  it("opens the preview drawer when the preview action is clicked", () => {
    render(
      <SlotFile
        slot={makeSlot({ artifact_value: { url: "/f.pdf", filename: "f.pdf" } })}
      />,
    );
    fireEvent.click(screen.getByText("chat.slots.preview"));
    expect(screen.getByTestId("file-preview-drawer")).toHaveTextContent("f.pdf");
  });
});

describe("SlotText", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows a pending placeholder while no artifact value is present", () => {
    render(<SlotText slot={makeSlot({ artifact_value: undefined })} />);
    expect(screen.getByText("chat.slots.pendingCalculation")).toBeInTheDocument();
  });

  it("renders plain text content and enters edit mode when clicked (editable)", () => {
    render(
      <SlotText
        slot={makeSlot({ artifact_value: { text: "hello world" }, list_index: 0 })}
        sessionId="sess-1"
        slotId="slot-1"
      />,
    );
    const textEl = screen.getByText("hello world");
    fireEvent.click(textEl);
    expect(screen.getByRole("textbox", { name: "chat.slots.editText" })).toHaveValue("hello world");
  });

  it("is not clickable/editable when readOnly is true", () => {
    render(
      <SlotText
        slot={makeSlot({ artifact_value: { text: "hello world" } })}
        sessionId="sess-1"
        slotId="slot-1"
        readOnly
      />,
    );
    const textEl = screen.getByText("hello world");
    expect(textEl).not.toHaveAttribute("role", "button");
  });

  it("saves an edited draft on blur and calls draftStore.setDraft", async () => {
    const { draftStore } = await import("@/modules/chat/store/pluginPanel");
    render(
      <SlotText
        slot={makeSlot({ artifact_value: { text: "hello" }, list_index: 0 })}
        sessionId="sess-1"
        slotId="slot-1"
      />,
    );
    fireEvent.click(screen.getByText("hello"));
    const textarea = screen.getByRole("textbox", { name: "chat.slots.editText" });
    fireEvent.change(textarea, { target: { value: "hello edited" } });
    fireEvent.blur(textarea);
    expect(draftStore.setDraft).toHaveBeenCalledWith(
      "sess-1",
      "slot-1",
      0,
      expect.objectContaining({ text: "hello edited" }),
      0,
    );
  });
});

describe("SlotImage", () => {
  it("shows a pending placeholder when there is no source path", () => {
    render(<SlotImage slot={makeSlot({ artifact_value: {} })} />);
    expect(screen.getByText("chat.slots.inProgress")).toBeInTheDocument();
  });
});

describe("AddSlotItemButton", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("opens a text input modal for text slots and submits new content", async () => {
    render(<AddSlotItemButton sessionId="sess-1" slotId="slot-1" slotType="text" onCreated={vi.fn()} />);
    fireEvent.click(screen.getByLabelText("chat.slots.addItem"));

    const textarea = screen.getByLabelText("chat.slots.itemContent");
    fireEvent.change(textarea, { target: { value: "new text" } });
    // Ctrl+Enter on the textarea triggers submit directly.
    fireEvent.keyDown(textarea, { key: "Enter", ctrlKey: true });

    await waitFor(() =>
      expect(mockCreateSlotItem).toHaveBeenCalledWith("sess-1", "slot-1", { text: "new text" }, undefined, undefined, "text"),
    );
  });

  it("triggers the native file picker directly for image slots instead of opening a modal", () => {
    render(<AddSlotItemButton sessionId="sess-1" slotId="slot-1" slotType="image" onCreated={vi.fn()} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const clickSpy = vi.spyOn(input, "click");
    fireEvent.click(screen.getByLabelText("chat.slots.addItem"));
    expect(clickSpy).toHaveBeenCalled();
  });

  it("uploads the selected file and calls createSlotItem for file slots", async () => {
    mockUploadFileInChunks.mockResolvedValue("uploads/new-file.txt");
    render(<AddSlotItemButton sessionId="sess-1" slotId="slot-1" slotType="file" onCreated={vi.fn()} />);
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["content"], "new-file.txt", { type: "text/plain" });
    fireEvent.change(input, { target: { files: [file] } });

    await waitFor(() =>
      expect(mockCreateSlotItem).toHaveBeenCalledWith(
        "sess-1",
        "slot-1",
        { path: "uploads/new-file.txt" },
        undefined,
        undefined,
        "file",
      ),
    );
  });
});

describe("SlotRenderer", () => {
  it("renders a pending placeholder when artifact_value is missing", () => {
    render(<SlotRenderer slot={makeSlot({ artifact_value: undefined })} expectedType="text" />);
    expect(screen.getByText("chat.slots.pendingCalculation")).toBeInTheDocument();
  });

  it("dispatches to SlotImage for image content types", () => {
    render(
      <SlotRenderer
        slot={makeSlot({ content_type: "image", artifact_value: {} })}
        expectedType="image"
      />,
    );
    expect(screen.getByText("chat.slots.inProgress")).toBeInTheDocument();
  });

  it("dispatches to SlotFile for generic file content types", () => {
    render(
      <SlotRenderer
        slot={makeSlot({ content_type: "file", artifact_value: { url: "/x.zip", filename: "x.zip" } })}
        expectedType="file"
      />,
    );
    expect(screen.getByText("x.zip")).toBeInTheDocument();
  });

  it("falls back to SlotText for plain text content", () => {
    render(
      <SlotRenderer
        slot={makeSlot({ content_type: "text", artifact_value: { text: "plain content" } })}
        expectedType="text"
      />,
    );
    expect(screen.getByText("plain content")).toBeInTheDocument();
  });
});
