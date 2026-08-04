import { fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import NewPluginModal from "./index";
import {
  createPluginDraft,
  aiGeneratePluginDraft,
  updatePluginDraftContent,
  deletePluginDraft,
} from "../../pluginDraftApi";
import { listSkillAssetsPage } from "@/modules/memory/skillApi";

vi.mock("../../pluginDraftApi", () => ({
  createPluginDraft: vi.fn(),
  aiGeneratePluginDraft: vi.fn(),
  updatePluginDraftContent: vi.fn(),
  deletePluginDraft: vi.fn(),
}));

vi.mock("@/modules/memory/skillApi", () => ({
  listSkillAssetsPage: vi.fn(),
}));

const createPluginDraftMock = createPluginDraft as ReturnType<typeof vi.fn>;
const aiGeneratePluginDraftMock = aiGeneratePluginDraft as ReturnType<typeof vi.fn>;
const updatePluginDraftContentMock = updatePluginDraftContent as ReturnType<typeof vi.fn>;
const deletePluginDraftMock = deletePluginDraft as ReturnType<typeof vi.fn>;
const listSkillAssetsPageMock = listSkillAssetsPage as ReturnType<typeof vi.fn>;

describe("NewPluginModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    createPluginDraftMock.mockResolvedValue({ id: "draft-1", version: 1 });
    updatePluginDraftContentMock.mockResolvedValue({ id: "draft-1", version: 1 });
    aiGeneratePluginDraftMock.mockResolvedValue({ id: "draft-1", version: 1 });
    listSkillAssetsPageMock.mockResolvedValue({ records: [], total: 0 });
  });

  it("does not render its body when closed", () => {
    renderWithProviders(
      <NewPluginModal open={false} onCancel={vi.fn()} onCreated={vi.fn()} />,
    );
    expect(screen.queryByText("selfEvolutionRun.newPluginModalTitle")).not.toBeInTheDocument();
  });

  it("shows the ai description textarea by default and requires a plugin id before creating", async () => {
    renderWithProviders(
      <NewPluginModal open onCancel={vi.fn()} onCreated={vi.fn()} />,
    );

    expect(screen.getByText("selfEvolutionRun.newPluginModalTitle")).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText("selfEvolutionRun.newPluginAiPlaceholder"),
    ).toBeInTheDocument();

    const createBtn = screen.getByRole("button", { name: "selfEvolutionRun.newPluginCreateBtn" });
    expect(createBtn).toBeDisabled();
  });

  it("creates a draft with the trimmed plugin id and ai description on submit", async () => {
    const onCreated = vi.fn();
    renderWithProviders(
      <NewPluginModal open onCancel={vi.fn()} onCreated={onCreated} />,
    );

    fireEvent.change(
      screen.getByPlaceholderText("selfEvolutionRun.newPluginAiPlaceholder"),
      { target: { value: "Summarize meeting notes" } },
    );
    fireEvent.change(
      screen.getByPlaceholderText("selfEvolutionRun.newPluginFieldPluginIdPlaceholder"),
      { target: { value: " my-plugin " } },
    );

    const createBtn = screen.getByRole("button", { name: "selfEvolutionRun.newPluginCreateBtn" });
    await waitFor(() => expect(createBtn).not.toBeDisabled());
    fireEvent.click(createBtn);

    await waitFor(() => expect(createPluginDraftMock).toHaveBeenCalledWith({
      name: "my-plugin",
      source_type: "ai",
    }));
    await waitFor(() => expect(aiGeneratePluginDraftMock).toHaveBeenCalledWith("draft-1", {
      description: "Summarize meeting notes",
    }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("draft-1"));
    expect(deletePluginDraftMock).not.toHaveBeenCalled();
  });

  it("rejects an invalid plugin id and shows an error instead of creating", async () => {
    renderWithProviders(<NewPluginModal open onCancel={vi.fn()} onCreated={vi.fn()} />);

    fireEvent.change(
      screen.getByPlaceholderText("selfEvolutionRun.newPluginFieldPluginIdPlaceholder"),
      { target: { value: "1-bad-id" } },
    );

    expect(
      screen.getByText("selfEvolutionRun.newPluginIdErrorInvalid"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "selfEvolutionRun.newPluginCreateBtn" }),
    ).toBeDisabled();
    expect(createPluginDraftMock).not.toHaveBeenCalled();
  });

  it("cleans up the created draft when a later step in creation fails", async () => {
    updatePluginDraftContentMock.mockRejectedValueOnce(new Error("network error"));
    deletePluginDraftMock.mockResolvedValueOnce(undefined);

    renderWithProviders(<NewPluginModal open onCancel={vi.fn()} onCreated={vi.fn()} />);

    fireEvent.change(
      screen.getByPlaceholderText("selfEvolutionRun.newPluginAiPlaceholder"),
      { target: { value: "desc" } },
    );
    fireEvent.change(
      screen.getByPlaceholderText("selfEvolutionRun.newPluginFieldPluginIdPlaceholder"),
      { target: { value: "my-plugin" } },
    );

    fireEvent.click(screen.getByRole("button", { name: "selfEvolutionRun.newPluginCreateBtn" }));

    await waitFor(() => expect(deletePluginDraftMock).toHaveBeenCalledWith("draft-1"));
  });

  it("switches to skill mode and lists matching skills when the picker gains focus", async () => {
    listSkillAssetsPageMock.mockResolvedValueOnce({
      records: [{ id: "skill-1", name: "My Skill" }],
      total: 1,
    });

    renderWithProviders(
      <NewPluginModal open onCancel={vi.fn()} onCreated={vi.fn()} />,
    );

    fireEvent.click(screen.getByText("selfEvolutionRun.newPluginModeSkillTitle"));

    // antd's Modal renders into a body-level portal, so query from `document`
    // rather than the RTL `container` (which only wraps the original mount point).
    const selectInput = document.querySelector(".ant-select-selection-search-input");
    expect(selectInput).toBeTruthy();
    fireEvent.focus(selectInput!);

    await waitFor(() => expect(listSkillAssetsPageMock).toHaveBeenCalledWith(
      expect.objectContaining({ keyword: "" }),
    ));
  });
});
