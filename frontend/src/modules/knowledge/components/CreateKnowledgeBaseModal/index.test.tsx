import { createRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider, message } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import CreateKnowledgeBaseModal, { type CreateKnowledgeBaseModalRef } from "./index";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";
import { saveKnowledgeBaseCapabilities, type LearningCatalog } from "@/modules/learning/api";
import { getKnowledgeOptions, type KnowledgeOptions } from "@/modules/knowledge/utils/request";
import { isDeveloperModeActive } from "@/utils/developerMode";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/modules/dataSource/components/management/DataSourceProviderPicker", () => ({ default: () => null }));
vi.mock("@/utils/developerMode", () => ({ isDeveloperModeActive: vi.fn() }));
vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: vi.fn().mockResolvedValue({ document_parsing_enabled: false }),
}));
vi.mock("@/modules/knowledge/utils/request", () => ({
  getKnowledgeOptions: vi.fn(),
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
  ],
  capabilities: generalKeys.map((key) => ({
    key, version: 1, name_i18n_key: key, description_i18n_key: "", local_only: true,
    languages: ["*"], subject_kinds: [], fields: [], provider_pipeline: [],
    allowed_question_types: [], default_question_types: [],
    cache_policy: { default_scope: "user_global", allowed_scopes: ["user_global"], context_sensitive: false },
  })),
};

const options: KnowledgeOptions = {
 scenes: catalog.profiles, profiles: [], capabilities: catalog.capabilities, local_available: true,
 algorithms: [{algo_id: "test-algo"}], processing_levels: ["stored"], default_processing_level: "stored",
 default_scene: "general", default_algo_id: "test-algo", upload: {max_files:20,max_file_bytes:500*1024*1024,max_batch_bytes:1024*1024*1024,file_types:["pdf"]},
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(isDeveloperModeActive).mockReturnValue(false);
  vi.mocked(getKnowledgeOptions).mockResolvedValue(options);
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

describe("knowledge creation capability submission", () => {
  it("keeps the settings of a selected custom preset", async () => {
    vi.mocked(getKnowledgeOptions).mockResolvedValue({
      ...options,
      profiles: [{ id: "preset-translation", custom_name: "Translation preset",
        capability_refs_json: JSON.stringify([{ key: "general_translation", version: 1,
          enabled: true, settings: { target_language: "en" } }]) }] as KnowledgeOptions["profiles"],
    });
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    const profiles = screen.getByRole("combobox", { name: "learning.capabilityProfile" });
    fireEvent.mouseDown(profiles);
    fireEvent.click(await screen.findByText("Translation preset"));
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      scene: "custom", profile_id: "preset-translation",
      reading_capabilities: [expect.objectContaining({ key: "general_translation", settings: { target_language: "en" } })],
    })));
  });

  it.each([false, true])("submits the general preset without opening advanced settings (developer=%s)", async (developer) => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(developer);
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    if (!developer) expect(screen.queryByText("learning.advancedSettings")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ scene: "general", reading_capabilities: generalKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })) })));
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ processing_level: "stored" }));
  });

  it.each([false, true])("submits a changed scenario's preset without opening advanced settings (developer=%s)", async (developer) => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(developer);
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    await selectEnglishProfile();
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ scene: "english_learning", reading_capabilities: englishKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })) })));
  });

  it("preserves advanced edits after the panel is collapsed", async () => {
    vi.mocked(isDeveloperModeActive).mockReturnValue(true);
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    fireEvent.click(screen.getByText("learning.advancedSettings"));
    fireEvent.change(await screen.findByRole("spinbutton", { name: /learning.maxSelectionLength/ }), {
      target: { value: "321" },
    });
    fireEvent.click(screen.getByText("learning.advancedSettings"));
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ reading_capabilities: [
      expect.objectContaining({ key: "chinese_definition", settings: expect.objectContaining({ max_selection_length: 321 }) }),
      expect.objectContaining({ key: "english_definition" }),
      expect.objectContaining({ key: "general_translation" }),
    ] })));
  });

  it("waits for the preset catalog before allowing creation", async () => {
    let resolveCatalog!: (value: KnowledgeOptions) => void;
    vi.mocked(getKnowledgeOptions).mockReturnValue(new Promise((resolve) => { resolveCatalog = resolve; }));
    const { onCreate } = await mountModal();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(onCreate).not.toHaveBeenCalled();
    await act(async () => resolveCatalog(options));
    await fillRequiredFields();
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ scene: "general", reading_capabilities: generalKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })) })));
  });

  it("does not create with missing presets when catalog loading fails, and allows retry", async () => {
    const error = new Error("catalog unavailable");
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      vi.mocked(getKnowledgeOptions).mockRejectedValueOnce(error);
      const { onCreate, ref } = await mountModal();
      await waitFor(() => expect(log).toHaveBeenCalledWith("Failed to load knowledge base creation form:", error));
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(onCreate).not.toHaveBeenCalled();
      expect(saveKnowledgeBaseCapabilities).not.toHaveBeenCalled();
      await act(async () => ref.current!.onOpen());
      await fillRequiredFields();
      fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
      await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ reading_capabilities: generalKeys.map((key, index) => ({ key, version: 1, enabled: true, display_order: index + 1, settings: {} })) })));
    } finally {
      log.mockRestore();
    }
  });

  it("rejects missing capabilities before creating a dataset", async () => {
    vi.mocked(getKnowledgeOptions).mockResolvedValue({
      ...options,
      scenes: [{ ...catalog.profiles[0], capabilities: [] }],
    });
    const { onCreate } = await mountModal();
    await fillRequiredFields();
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
    await screen.findByText("learning.selectCapabilities");
    expect(onCreate).not.toHaveBeenCalled();
    expect(saveKnowledgeBaseCapabilities).not.toHaveBeenCalled();
  });

  it("only reports success after the atomic create returns", async () => {
    let resolveSave!: () => void;
    const success = vi.spyOn(message, "success");
    try {
      const { onCreate } = await mountModal();
      onCreate.mockReturnValue(new Promise<void>(resolve => { resolveSave = resolve; }));
      await fillRequiredFields();
      fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));
      await waitFor(() => expect(onCreate).toHaveBeenCalled());
      expect(success).not.toHaveBeenCalled();
      await act(async () => resolveSave());
      await waitFor(() => expect(success).toHaveBeenCalledWith("knowledge.createSuccess"));
    } finally {
      success.mockRestore();
      message.destroy();
    }
  });
});
