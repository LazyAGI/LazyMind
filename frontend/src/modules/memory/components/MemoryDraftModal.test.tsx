import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import MemoryDraftModal from "./MemoryDraftModal";

const t = (key: string) => key;

const baseProps = {
  t,
  modalOpen: true,
  modalTitle: "Draft title",
  closeModal: vi.fn(),
  saveDraft: vi.fn(),
  experienceSaving: false,
  glossarySaving: false,
  skillSaving: false,
  isReadOnly: false,
  pendingGlossaryMergeSourceIds: [],
  tagOptions: [],
  normalizeTagValues: (values: string[]) => values,
  handleImportSkillPackage: vi.fn(),
};

describe("MemoryDraftModal", () => {
  it("renders experience fields and updates the draft on change", () => {
    const setDraft = vi.fn();
    renderWithProviders(
      <MemoryDraftModal
        {...baseProps}
        activeTab="experience"
        modalMode="add"
        draft={{ title: "", content: "" }}
        setDraft={setDraft}
      />,
    );

    expect(screen.getByText("admin.memoryTitle")).toBeInTheDocument();
    expect(screen.getByText("admin.memoryContent")).toBeInTheDocument();

    const titleInput = screen.getByPlaceholderText(
      "common.pleaseInputadmin.memoryTitle",
    );
    fireEvent.change(titleInput, { target: { value: "new title" } });
    expect(setDraft).toHaveBeenCalled();
  });

  it("renders glossary fields with term input", () => {
    renderWithProviders(
      <MemoryDraftModal
        {...baseProps}
        activeTab="glossary"
        modalMode="add"
        draft={{ term: "", aliases: [], content: "" }}
        setDraft={vi.fn()}
      />,
    );

    expect(screen.getByText("admin.memoryGlossaryTerm")).toBeInTheDocument();
    expect(screen.getByText("admin.memoryGlossaryAliases")).toBeInTheDocument();
  });

  it("shows the merge hint alert when pendingGlossaryMergeSourceIds is non-empty", () => {
    renderWithProviders(
      <MemoryDraftModal
        {...baseProps}
        activeTab="glossary"
        modalMode="add"
        pendingGlossaryMergeSourceIds={["a", "b"]}
        draft={{ term: "", aliases: [], content: "" }}
        setDraft={vi.fn()}
      />,
    );

    expect(
      screen.getByText("admin.memoryGlossaryBatchMergeDraftHint"),
    ).toBeInTheDocument();
  });

  it("renders skill create fields (name, description, category, tags)", () => {
    renderWithProviders(
      <MemoryDraftModal
        {...baseProps}
        activeTab="skills"
        modalMode="add"
        draft={{ name: "", description: "", category: "", tags: [], content: "" }}
        setDraft={vi.fn()}
      />,
    );

    expect(screen.getByText("admin.memoryName")).toBeInTheDocument();
    expect(screen.getByText("admin.memoryCategory")).toBeInTheDocument();
    expect(screen.getByText("admin.memoryMarkdown")).toBeInTheDocument();
  });

  it("shows an import alert instead of the markdown editor when a package file is pending", () => {
    const file = new File(["content"], "skill.zip", { type: "application/zip" });
    renderWithProviders(
      <MemoryDraftModal
        {...baseProps}
        activeTab="skills"
        modalMode="add"
        draft={{ name: "", description: "", category: "", tags: [], content: "" }}
        setDraft={vi.fn()}
        pendingSkillPackageFile={file}
      />,
    );

    expect(screen.getByText("admin.memorySkillUploadFileTitle")).toBeInTheDocument();
    expect(screen.queryByText("admin.memoryMarkdown")).not.toBeInTheDocument();
  });

  it("hides category/tags and shows the metadata hint alert for skill edit mode", () => {
    renderWithProviders(
      <MemoryDraftModal
        {...baseProps}
        activeTab="skills"
        modalMode="edit"
        draft={{ name: "n", description: "d", category: "c", tags: [], content: "" }}
        setDraft={vi.fn()}
      />,
    );

    expect(screen.getByText("admin.memorySkillEditMetadataHint")).toBeInTheDocument();
    expect(screen.queryByText("admin.memoryCategory")).not.toBeInTheDocument();
  });

  it("uses the close label and marks readonly styling when isReadOnly is true", () => {
    renderWithProviders(
      <MemoryDraftModal
        {...baseProps}
        activeTab="experience"
        modalMode="view"
        isReadOnly
        draft={{ title: "t", content: "c" }}
        setDraft={vi.fn()}
      />,
    );

    expect(screen.getByText("common.close")).toBeInTheDocument();
  });
});
