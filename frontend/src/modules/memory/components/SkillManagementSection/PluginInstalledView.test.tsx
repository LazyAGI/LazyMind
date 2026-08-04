import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import PluginInstalledView from "./PluginInstalledView";
import {
  listPluginDrafts,
  deletePluginDraft,
  listBuiltinPlugins,
  listUserPluginSettings,
} from "@/modules/plugin/pluginDraftApi";

vi.mock("@/modules/plugin/pluginDraftApi", () => ({
  listPluginDrafts: vi.fn(),
  deletePluginDraft: vi.fn(),
  updatePluginDraftContent: vi.fn(),
  listBuiltinPlugins: vi.fn(),
  listUserPluginSettings: vi.fn(),
  setUserPluginCallMode: vi.fn(),
}));

vi.mock(
  "@/modules/plugin/components/StateGraphEditor/PluginInfoModal",
  () => ({
    default: () => null,
  }),
);

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const draftRecord = {
  id: "draft-1",
  name: "My Draft",
  content: "",
  plugin_yaml_content: "id: draft-plugin",
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
  created_at: "2024-01-01T00:00:00Z",
  updated_at: "2024-01-02T00:00:00Z",
  created_by: "tester",
  published: false,
  published_plugin_ref: "",
  current_revision_id: "",
  current_revision_no: 0,
  published_status: "",
  base_revision_id: "",
  draft_dirty: false,
  last_repair_run_id: "",
};

describe("PluginInstalledView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (listPluginDrafts as any).mockResolvedValue({ records: [draftRecord], total: 1 });
    (listBuiltinPlugins as any).mockResolvedValue([]);
    (listUserPluginSettings as any).mockResolvedValue([]);
    (deletePluginDraft as any).mockResolvedValue(undefined);
  });

  const renderView = (onNewPlugin = vi.fn()) =>
    render(
      <MemoryRouter>
        <PluginInstalledView t={t} onNewPlugin={onNewPlugin} />
      </MemoryRouter>,
    );

  it("loads and renders draft plugin rows", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByText("My Draft")).toBeInTheDocument();
    });
  });

  it("shows an empty state with a create button when there are no plugins", async () => {
    (listPluginDrafts as any).mockResolvedValue({ records: [], total: 0 });
    const onNewPlugin = vi.fn();
    renderView(onNewPlugin);
    await waitFor(() => {
      expect(screen.getByText("admin.memoryPluginEmptyDesc")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("admin.memoryPluginNewButton"));
    expect(onNewPlugin).toHaveBeenCalledTimes(1);
  });

  it("filters rows by search query", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByText("My Draft")).toBeInTheDocument();
    });
    fireEvent.change(
      screen.getByPlaceholderText("admin.memoryPluginSearchPlaceholder"),
      { target: { value: "no-match" } },
    );
    fireEvent.keyDown(
      screen.getByPlaceholderText("admin.memoryPluginSearchPlaceholder"),
      { key: "Enter", code: "Enter", keyCode: 13, which: 13 },
    );
    await waitFor(() => {
      expect(screen.queryByText("My Draft")).not.toBeInTheDocument();
    });
  });

  it("deletes a draft when confirming the delete popconfirm", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByText("My Draft")).toBeInTheDocument();
    });
    const deleteButton = document.querySelector(
      "button .anticon-delete",
    )?.closest("button");
    expect(deleteButton).not.toBeNull();
    fireEvent.click(deleteButton as HTMLButtonElement);

    const confirmButton = await screen.findByText("admin.memoryPluginDeleteOk");
    fireEvent.click(confirmButton);

    await waitFor(() => {
      expect(deletePluginDraft).toHaveBeenCalledWith("draft-1");
    });
  });
});
