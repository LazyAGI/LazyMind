import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider, message } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import CreateKnowledgeBaseModal, { type CreateKnowledgeBaseModalRef } from "./index";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import { createCapabilityProfile, getLearningCatalog, listCapabilityProfiles, saveKnowledgeBaseCapabilities, type LearningCatalog } from "@/modules/learning/api";
import { isDeveloperModeActive } from "@/utils/developerMode";
import { fetchUserUiPreferences } from "@/modules/user/uiPreferencesApi";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
const rag = vi.hoisted(() => ({ availability: "ready" }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({ availability: rag.availability }),
}));
vi.mock("@/modules/dataSource/components/management/DataSourceProviderPicker", () => ({ default: () => null }));
vi.mock("@/utils/developerMode", () => ({ isDeveloperModeActive: vi.fn() }));
vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: vi.fn().mockResolvedValue({ document_parsing_enabled: false }),
}));
vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceAllDatasetTags: vi.fn().mockResolvedValue({ data: { tags: ["test-tag"] } }),
    datasetServiceListAlgos: vi.fn().mockResolvedValue({ data: { algos: [{ algo_id: "test-algo" }] } }),
  }),
}));
vi.mock("@/modules/learning/api", () => ({
  getLearningCatalog: vi.fn(),
  listCapabilityProfiles: vi.fn().mockResolvedValue({ custom: [] }),
  createCapabilityProfile: vi.fn(),
  saveKnowledgeBaseCapabilities: vi.fn(),
}));

const generalKeys = ["chinese_definition", "english_definition", "general_translation"];
const englishKeys = ["english_definition", "general_translation"];
const catalog: LearningCatalog = {
  local_available: true,
  question_types: [],
  profiles: [
    { key: "general", name_i18n_key: "General reading", description_i18n_key: "", capabilities: generalKeys },
    { key: "english_learning", name_i18n_key: "English learning", description_i18n_key: "", capabilities: englishKeys },
    { key: "academic_papers", name_i18n_key: "Academic papers", description_i18n_key: "", capabilities: generalKeys },
  ],
  capabilities: generalKeys.map((key) => ({
    key, version: 1, name_i18n_key: key, description_i18n_key: "", local_only: true,
    languages: ["*"], subject_kinds: [], fields: [], provider_pipeline: [],
    allowed_question_types: [], default_question_types: [],
    cache_policy: { default_scope: "user_global", allowed_scopes: ["user_global"], context_sensitive: false },
  })),
};

beforeEach(() => {
  vi.clearAllMocks();
  rag.availability = "ready";
  vi.mocked(fetchUserUiPreferences).mockResolvedValue({ document_parsing_enabled: false } as Awaited<ReturnType<typeof fetchUserUiPreferences>>);
  localStorage.clear();
  vi.mocked(isDeveloperModeActive).mockReturnValue(false);
  vi.mocked(getLearningCatalog).mockResolvedValue(catalog);
  vi.mocked(listCapabilityProfiles).mockResolvedValue({ builtin: catalog.profiles, custom: [] });
  vi.mocked(saveKnowledgeBaseCapabilities).mockResolvedValue({});
});
afterEach(() => {
  cleanup();
  message.destroy();
});

async function mountModal() {
  const ref = createRef<CreateKnowledgeBaseModalRef>();
  const onCreate = vi.fn().mockResolvedValue({ dataset_id: "test-dataset" });
  render(<ConfigProvider theme={{ token: { motion: false } }}>
    <CreateKnowledgeBaseModal ref={ref} onCreate={onCreate}
      syncCreateVm={{} as SyncKnowledgeBaseCreationVm} embeddingReady />
  </ConfigProvider>);
  await act(async () => ref.current!.onOpen());
  return { onCreate, ref };
}

async function fillRequiredFields() {
  fireEvent.change(await screen.findByLabelText(/knowledge.knowledgeBaseName/), {
    target: { value: "test-knowledge" },
  });
  const tags = screen.getByRole("combobox", { name: "" });
  fireEvent.mouseDown(tags);
  fireEvent.keyDown(tags, { key: "ArrowDown", keyCode: 40 });
  fireEvent.keyDown(tags, { key: "Enter", keyCode: 13 });
}

async function selectEnglishProfile() {
  const profiles = screen.getByRole("combobox", { name: "learning.capabilityProfile" });
  fireEvent.mouseDown(profiles);
  fireEvent.keyDown(profiles, { key: "ArrowDown", keyCode: 40 });
  fireEvent.keyDown(profiles, { key: "Enter", keyCode: 13 });
}

async function selectProfile(label: string) {
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "learning.capabilityProfile" }));
  fireEvent.click(await screen.findByText(label, { selector: ".ant-select-item-option-content" }));
}

describe("knowledge creation capability submission", () => {
  it("preserves the indexing target for deferred ingestion without RAG", async () => {
    rag.availability = "missing";
    vi.mocked(fetchUserUiPreferences).mockResolvedValue({ document_parsing_enabled: true } as Awaited<ReturnType<typeof fetchUserUiPreferences>>);
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    expect(screen.getByRole("tab", { name: "knowledge.createFromCloudDisk" })).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ processing_level: "indexed" })));
  });

  it.each([false, true])("submits the general preset without opening advanced settings (developer=%s)", async (developer) => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(developer);
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    if (!developer) expect(screen.queryByText("learning.advancedSettings")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(saveKnowledgeBaseCapabilities).toHaveBeenCalledWith("test-dataset",
      generalKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })),
    ));
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ processing_level: "stored", library_kind: "document", scenario_key: "general" }));
  });

  it.each([false, true])("derives the paper kind only from the academic scenario (developer=%s)", async (developer) => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(developer);
    vi.mocked(fetchUserUiPreferences).mockResolvedValue({ document_parsing_enabled: true } as Awaited<ReturnType<typeof fetchUserUiPreferences>>);
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    expect(screen.queryByText("资料库类型")).not.toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: "论文集" })).not.toBeInTheDocument();
    vi.mocked(fetchUserUiPreferences).mockResolvedValue({ document_parsing_enabled: true } as Awaited<ReturnType<typeof fetchUserUiPreferences>>);
    await selectProfile("Academic papers");
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ library_kind: "paper" })));
    expect(onCreate.mock.calls[0][0]).not.toHaveProperty("learning_profile_key");
  });

  it.each(["General reading", "English learning"])("creates a document library when switching from academic papers to %s", async (scenario) => {
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    vi.mocked(fetchUserUiPreferences).mockResolvedValue({ document_parsing_enabled: true } as Awaited<ReturnType<typeof fetchUserUiPreferences>>);
    await selectProfile("Academic papers");
    await selectProfile(scenario);
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ library_kind: "document" })));
    const keys = scenario === "English learning" ? englishKeys : generalKeys;
    await waitFor(() => expect(saveKnowledgeBaseCapabilities).toHaveBeenCalledWith("test-dataset",
      keys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })),
    ));
  });

  it.each([false, true])("submits a changed scenario's preset without opening advanced settings (developer=%s)", async (developer) => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(developer);
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    await selectEnglishProfile();
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(saveKnowledgeBaseCapabilities).toHaveBeenCalledWith("test-dataset",
      englishKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })),
    ));
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ scenario_key: "english_learning" }));
  });

  it("preserves advanced edits after the panel is collapsed", async () => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(true);
    await mountModal();
    await fillRequiredFields();
    fireEvent.click(screen.getByText("learning.advancedSettings"));
    fireEvent.change(await screen.findByRole("spinbutton", { name: /learning.maxSelectionLength/ }), {
      target: { value: "321" },
    });
    fireEvent.click(screen.getByText("learning.advancedSettings"));
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(saveKnowledgeBaseCapabilities).toHaveBeenCalledWith("test-dataset", [
      expect.objectContaining({ key: "chinese_definition", settings: expect.objectContaining({ max_selection_length: 321 }) }),
      expect.objectContaining({ key: "english_definition" }),
      expect.objectContaining({ key: "general_translation" }),
    ]));
  });

  it("persists an existing custom preset identity and its capabilities", async () => {
    vi.mocked(listCapabilityProfiles).mockResolvedValue({ builtin: catalog.profiles, custom: [{ id: "custom-owned", custom_name: "My preset", description: "", capability_refs_json: JSON.stringify(englishKeys.map(key => ({ key }))) }] });
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    const profiles = screen.getByRole("combobox", { name: "learning.capabilityProfile" });
    fireEvent.mouseDown(profiles);
    fireEvent.click(screen.getByText("My preset"));
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ scenario_key: "custom-owned" })));
    expect(createCapabilityProfile).not.toHaveBeenCalled();
    await waitFor(() => expect(saveKnowledgeBaseCapabilities).toHaveBeenCalledWith("test-dataset", englishKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} }))));
  });

  it("uses the saved identity when creating a custom preset", async () => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(true);
    vi.mocked(createCapabilityProfile).mockResolvedValue({ id: "new-custom", custom_name: "New preset", description: "", capability_refs_json: "[]" });
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "learning.capabilityProfile" }));
    fireEvent.click(screen.getByText("learning.customProfile"));
    fireEvent.change(await screen.findByLabelText(/learning.customProfileName/), { target: { value: "New preset" } });
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ scenario_key: "new-custom" })));
  });

  it("waits for the preset catalog before allowing creation", async () => {
    let resolveCatalog!: (value: LearningCatalog) => void;
    vi.mocked(getLearningCatalog).mockReturnValue(new Promise((resolve) => { resolveCatalog = resolve; }));
    const { onCreate } = await mountModal();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(onCreate).not.toHaveBeenCalled();
    await act(async () => resolveCatalog(catalog));
    await fillRequiredFields();
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(saveKnowledgeBaseCapabilities).toHaveBeenCalledWith("test-dataset",
      generalKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })),
    ));
  });

  it("does not create with missing presets when catalog loading fails, and allows retry", async () => {
    const error = new Error("catalog unavailable");
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      vi.mocked(getLearningCatalog).mockRejectedValueOnce(error);
      const { onCreate, ref } = await mountModal();
      await waitFor(() => expect(log).toHaveBeenCalledWith("Failed to load knowledge base creation form:", error));
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(onCreate).not.toHaveBeenCalled();
      expect(saveKnowledgeBaseCapabilities).not.toHaveBeenCalled();
      await act(async () => ref.current!.onOpen());
      await fillRequiredFields();
      fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
      await waitFor(() => expect(saveKnowledgeBaseCapabilities).toHaveBeenCalledWith("test-dataset",
        generalKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })),
      ));
    } finally {
      log.mockRestore();
    }
  });

  it("rejects missing capabilities before creating a dataset", async () => {
    vi.mocked(getLearningCatalog).mockResolvedValue({
      ...catalog,
      profiles: [{ ...catalog.profiles[0], capabilities: [] }],
    });
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await screen.findByText("learning.selectCapabilities");
    expect(onCreate).not.toHaveBeenCalled();
    expect(saveKnowledgeBaseCapabilities).not.toHaveBeenCalled();
  });

  it("only reports creation success after capabilities have been saved", async () => {
    let resolveSave!: () => void;
    vi.mocked(saveKnowledgeBaseCapabilities).mockReturnValue(new Promise<void>((resolve) => { resolveSave = resolve; }));
    const success = vi.spyOn(message, "success");
    try {
      await mountModal();
      await fillRequiredFields();
      fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
      await waitFor(() => expect(saveKnowledgeBaseCapabilities).toHaveBeenCalled());
      expect(success).not.toHaveBeenCalled();
      await act(async () => resolveSave());
      await waitFor(() => expect(success).toHaveBeenCalledWith("knowledge.createSuccess"));
    } finally {
      success.mockRestore();
      message.destroy();
    }
  });
});
