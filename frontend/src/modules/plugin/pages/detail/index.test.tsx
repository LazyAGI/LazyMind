import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { testI18n } from "@/test/testUtils";
import PluginDetailPage from "./index";
import {
  getPluginDraft,
  listPluginDrafts,
  listPluginVersions,
} from "../../pluginDraftApi";
import type { PluginDraftRecord } from "../../pluginDraftApi";

vi.mock("../../pluginDraftApi", () => ({
  getPluginDraft: vi.fn(),
  listPluginDrafts: vi.fn(),
  updatePluginDraftContent: vi.fn(),
  aiGeneratePluginDraft: vi.fn(),
  repairPluginDraft: vi.fn(),
  publishPluginDraft: vi.fn(),
  listPluginVersions: vi.fn(),
  getPluginVersion: vi.fn(),
  editPluginVersion: vi.fn(),
  getPluginGenerationAnalysis: vi.fn(),
  confirmPluginWorkflow: vi.fn(),
  previewPluginRepair: vi.fn(),
  getPluginRepairRun: vi.fn(),
  validatePluginDraft: vi.fn(),
}));

vi.mock("../../components/StateGraphEditor", () => ({
  default: (props: { pluginName?: React.ReactNode; onClose?: () => void; initialStateYaml?: string }) => (
    <div data-testid="state-graph-editor">
      <div data-testid="editor-plugin-name">{props.pluginName}</div>
      <div data-testid="editor-initial-yaml">{props.initialStateYaml ?? ""}</div>
      <button onClick={props.onClose}>close-editor</button>
    </div>
  ),
}));

const getPluginDraftMock = getPluginDraft as ReturnType<typeof vi.fn>;
const listPluginDraftsMock = listPluginDrafts as ReturnType<typeof vi.fn>;
const listPluginVersionsMock = listPluginVersions as ReturnType<typeof vi.fn>;

function makeDraft(overrides: Partial<PluginDraftRecord> = {}): PluginDraftRecord {
  return {
    id: "plugin-1",
    name: "My Plugin",
    content: "",
    plugin_yaml_content: "",
    state_yaml_content: "",
    state_layout_content: "",
    scenario_content: "",
    scripts_content: "",
    generate_status: "done",
    generate_error: "",
    generate_warning: "",
    design_brief_content: "",
    source_type: "blank",
    source_skill_id: "",
    source_skill_name: "",
    source_skill_revision_id: "",
    source_skill_revision_no: 0,
    source_skill_tree_hash: "",
    source_analysis_id: "",
    version: 1,
    created_at: "",
    updated_at: "",
    created_by: "",
    published: false,
    published_plugin_ref: "",
    current_revision_id: "",
    current_revision_no: 0,
    published_status: "",
    base_revision_id: "",
    draft_dirty: false,
    last_repair_run_id: "",
    ...overrides,
  };
}

function renderPage(pluginId = "plugin-1") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[`/memory-management/plugins/${pluginId}`]}>
        <Routes>
          <Route path="/memory-management/plugins/:pluginId" element={<PluginDetailPage />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("PluginDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listPluginDraftsMock.mockResolvedValue({ records: [], total: 0 });
    listPluginVersionsMock.mockResolvedValue([]);
  });

  it("shows a loading spinner while the draft is being fetched", () => {
    getPluginDraftMock.mockReturnValue(new Promise(() => {}));
    const { container } = renderPage();
    expect(container.querySelector(".plugin-detail-loading")).toBeInTheDocument();
  });

  it("shows the not-found message when the draft fails to load", async () => {
    getPluginDraftMock.mockRejectedValue(new Error("not found"));
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.pluginDetailNotFound")).toBeInTheDocument();
    });
  });

  it("renders the state graph editor with the loaded draft content once ready", async () => {
    getPluginDraftMock.mockResolvedValue(
      makeDraft({ state_yaml_content: "steps: {}", plugin_yaml_content: 'name: "My Plugin"\n' }),
    );
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("state-graph-editor")).toBeInTheDocument();
    });
    expect(screen.getByTestId("editor-initial-yaml")).toHaveTextContent("steps: {}");
  });

  it("navigates back to the plugin list when the editor's close handler fires", async () => {
    getPluginDraftMock.mockResolvedValue(makeDraft());
    renderPage();
    await waitFor(() => expect(screen.getByTestId("state-graph-editor")).toBeInTheDocument());
    fireEvent.click(screen.getByText("close-editor"));
    // Navigation is asserted indirectly: the mocked editor unmounts once routed away
    // from the plugin detail route, since there's no matching Route for the list path.
    await waitFor(() => {
      expect(screen.queryByTestId("state-graph-editor")).not.toBeInTheDocument();
    });
  });

  it("shows the AI generation progress modal while generation is in progress", async () => {
    getPluginDraftMock.mockResolvedValue(makeDraft({ generate_status: "generating" }));
    renderPage();
    await waitFor(() => {
      expect(document.querySelector(".plugin-generate-progress-modal")).toBeInTheDocument();
    });
  });

  it("shows the failed banner with a regenerate action when generation failed", async () => {
    getPluginDraftMock.mockResolvedValue(makeDraft({ generate_status: "failed" }));
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.pluginDetailFailedBanner")).toBeInTheDocument();
    });
    expect(screen.getByText("selfEvolutionRun.pluginDetailRegenerate")).toBeInTheDocument();
  });
});
