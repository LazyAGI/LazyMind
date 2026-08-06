import { beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import {
  aiGeneratePluginDraft,
  confirmPluginWorkflow,
  createPluginDraft,
  deletePluginDraft,
  getBuiltinPlugin,
  getPluginDraft,
  getPluginRepairRun,
  getPluginVersion,
  listBuiltinPlugins,
  listPluginDrafts,
  listPluginVersions,
  listUserPluginSettings,
  polishPluginInfo,
  previewPluginRepair,
  publishPluginDraft,
  repairPluginDraft,
  setUserPluginCallMode,
  updatePluginDraftContent,
  validatePluginDraft,
} from "./pluginDraftApi";

vi.mock("@/components/request", () => ({
  BASE_URL: "",
  axiosInstance: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;
const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;
const mockedPatch = axiosInstance.patch as unknown as ReturnType<typeof vi.fn>;
const mockedDelete = axiosInstance.delete as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
});

describe("listPluginDrafts", () => {
  it("applies default page/pageSize and unwraps the response payload", async () => {
    mockedGet.mockResolvedValue({ data: { data: { records: [], total: 0 } } });

    const result = await listPluginDrafts();

    expect(mockedGet).toHaveBeenCalledWith("/api/core/plugin-drafts", {
      params: { page: 1, page_size: 20 },
    });
    expect(result).toEqual({ records: [], total: 0 });
  });

  it("forwards custom page/pageSize params", async () => {
    mockedGet.mockResolvedValue({ data: { data: { records: [], total: 5 } } });

    await listPluginDrafts({ page: 2, pageSize: 50 });

    expect(mockedGet).toHaveBeenCalledWith("/api/core/plugin-drafts", {
      params: { page: 2, page_size: 50 },
    });
  });
});

describe("createPluginDraft / getPluginDraft / updatePluginDraftContent", () => {
  it("posts the payload and returns the created draft", async () => {
    mockedPost.mockResolvedValue({ data: { data: { id: "d1", name: "Draft" } } });

    const result = await createPluginDraft({ name: "Draft" });

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts", { name: "Draft" });
    expect(result).toEqual({ id: "d1", name: "Draft" });
  });

  it("fetches a draft by id", async () => {
    mockedGet.mockResolvedValue({ data: { data: { id: "d1" } } });

    await getPluginDraft("d1");

    expect(mockedGet).toHaveBeenCalledWith("/api/core/plugin-drafts/d1", undefined);
  });

  it("wraps a legacy string payload into { content } before saving", async () => {
    mockedPost.mockResolvedValue({ data: { data: { id: "d1" } } });

    await updatePluginDraftContent("d1", "raw yaml text");

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts/d1:save", {
      content: "raw yaml text",
    });
  });

  it("forwards an object payload as-is when saving", async () => {
    mockedPost.mockResolvedValue({ data: { data: { id: "d1" } } });

    await updatePluginDraftContent("d1", { plugin_yaml_content: "id: p", version: 3 });

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts/d1:save", {
      plugin_yaml_content: "id: p",
      version: 3,
    });
  });
});

describe("deletePluginDraft / publishPluginDraft / validatePluginDraft", () => {
  it("issues a delete request for the draft id", async () => {
    mockedDelete.mockResolvedValue({});

    await deletePluginDraft("d1");

    expect(mockedDelete).toHaveBeenCalledWith("/api/core/plugin-drafts/d1");
  });

  it("publishes a draft and returns the published version", async () => {
    mockedPost.mockResolvedValue({ data: { data: { plugin_ref: "p1", revision_no: 1 } } });

    const result = await publishPluginDraft("d1");

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts/d1:publish");
    expect(result).toEqual({ plugin_ref: "p1", revision_no: 1 });
  });

  it("validates a draft with the editor profile", async () => {
    mockedPost.mockResolvedValue({ data: { data: { valid: true, diagnostics: [] } } });

    const result = await validatePluginDraft("d1");

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts/d1:validate", { profile: "editor" });
    expect(result.valid).toBe(true);
  });
});

describe("published version helpers", () => {
  it("lists versions for a plugin ref", async () => {
    mockedGet.mockResolvedValue({ data: { data: { versions: [{ revision_no: 1 }] } } });

    const result = await listPluginVersions("plugin:foo");

    expect(mockedGet).toHaveBeenCalledWith(
      "/api/core/published-plugins/plugin%3Afoo/versions",
    );
    expect(result).toEqual([{ revision_no: 1 }]);
  });

  it("fetches a specific version's content", async () => {
    mockedGet.mockResolvedValue({ data: { data: { revision_id: "r1" } } });

    await getPluginVersion("plugin:foo", "r1");

    expect(mockedGet).toHaveBeenCalledWith(
      "/api/core/published-plugins/plugin%3Afoo/versions/r1",
    );
  });
});

describe("listUserPluginSettings / setUserPluginCallMode", () => {
  it("lists user plugin settings", async () => {
    mockedGet.mockResolvedValue({ data: { data: { plugins: [{ plugin_ref: "p1" }] } } });

    const result = await listUserPluginSettings();

    expect(mockedGet).toHaveBeenCalledWith("/api/core/chat/settings/plugins");
    expect(result).toEqual([{ plugin_ref: "p1" }]);
  });

  it("encodes a builtin plugin ref with the builtin/ prefix path", async () => {
    mockedPatch.mockResolvedValue({});

    await setUserPluginCallMode("builtin:writer", "auto");

    expect(mockedPatch).toHaveBeenCalledWith(
      "/api/core/chat/settings/plugins/builtin/writer",
      { call_mode: "auto" },
    );
  });

  it("encodes a non-builtin plugin ref while preserving the colon", async () => {
    mockedPatch.mockResolvedValue({});

    await setUserPluginCallMode("plugin:foo", "manual");

    expect(mockedPatch).toHaveBeenCalledWith(
      "/api/core/chat/settings/plugins/plugin:foo",
      { call_mode: "manual" },
    );
  });
});

describe("aiGeneratePluginDraft / polishPluginInfo / confirmPluginWorkflow", () => {
  it("triggers AI generation with the description/skill_id payload", async () => {
    mockedPost.mockResolvedValue({ data: { data: { id: "d1", generate_status: "generating" } } });

    const result = await aiGeneratePluginDraft("d1", { description: "make a writer plugin" });

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts/d1:ai-generate", {
      description: "make a writer plugin",
    });
    expect(result.generate_status).toBe("generating");
  });

  it("polishes plugin info fields", async () => {
    mockedPost.mockResolvedValue({ data: { data: { description: "polished" } } });

    const result = await polishPluginInfo({
      fields: { description: "draft" },
      target_fields: ["description"],
    });

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts:polish-info", {
      fields: { description: "draft" },
      target_fields: ["description"],
    });
    expect(result).toEqual({ description: "polished" });
  });

  it("confirms a generated workflow candidate", async () => {
    mockedPost.mockResolvedValue({});

    await confirmPluginWorkflow("d1", {
      analysis_id: "a1",
      candidate_id: "c1",
      source_skill_revision_id: "s1",
      draft_version: 2,
    });

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts/d1:confirm-workflow", {
      analysis_id: "a1",
      candidate_id: "c1",
      source_skill_revision_id: "s1",
      draft_version: 2,
    });
  });
});

describe("repairPluginDraft / getPluginRepairRun", () => {
  it("triggers AI repair with the repair payload", async () => {
    mockedPost.mockResolvedValue({ data: { data: { id: "d1" } } });

    await repairPluginDraft("d1", { target: "statemachine", draft_version: 3 });

    expect(mockedPost).toHaveBeenCalledWith("/api/core/plugin-drafts/d1:ai-repair", {
      target: "statemachine",
      draft_version: 3,
    });
  });

  it("fetches a repair run by id", async () => {
    mockedGet.mockResolvedValue({ data: { data: { repair_id: "r1", status: "queued" } } });

    const result = await getPluginRepairRun("d1", "r1");

    expect(mockedGet).toHaveBeenCalledWith("/api/core/plugin-drafts/d1/repair-runs/r1");
    expect(result.status).toBe("queued");
  });
});

describe("previewPluginRepair", () => {
  it("filters diagnostics by target and normalizes Go-cased field names", async () => {
    mockedPost.mockResolvedValue({
      data: {
        data: {
          target: "statemachine",
          mode: "plugin_local",
          draft_version: 1,
          diagnostics: [
            { Code: "state_missing_start", Path: "steps[0]", Message: "no start", Severity: "error" },
            { code: "ui_missing_tab", path: "ui.tabs[0]", message: "no tab", severity: "warning" },
          ],
          planned_files: ["state.yml"],
        },
      },
    });

    const result = await previewPluginRepair("d1", { target: "statemachine", mode: "plugin_local" });

    expect(result.diagnostics).toEqual([
      { code: "state_missing_start", path: "steps[0]", message: "no start", severity: "error" },
    ]);
    expect(result.planned_files).toEqual(["state.yml"]);
  });

  it("includes plugin_yaml_invalid diagnostics regardless of the requested target", async () => {
    mockedPost.mockResolvedValue({
      data: {
        data: {
          diagnostics: [{ code: "plugin_yaml_invalid", path: "", message: "bad yaml", severity: "error" }],
        },
      },
    });

    const result = await previewPluginRepair("d1", { target: "ui", mode: "plugin_local" });

    expect(result.diagnostics).toHaveLength(1);
    expect(result.diagnostics[0].code).toBe("plugin_yaml_invalid");
  });

  it("deduplicates diagnostics with identical code/path/message/severity", async () => {
    const duplicateDiagnostic = { code: "state_missing_start", path: "a", message: "msg", severity: "error" };
    mockedPost.mockResolvedValue({
      data: { data: { diagnostics: [duplicateDiagnostic, duplicateDiagnostic] } },
    });

    const result = await previewPluginRepair("d1", { target: "statemachine", mode: "plugin_local" });

    expect(result.diagnostics).toHaveLength(1);
  });
});

describe("listBuiltinPlugins / getBuiltinPlugin", () => {
  it("unwraps the non-standard { plugins } response shape", async () => {
    mockedGet.mockResolvedValue({ data: { plugins: [{ id: "writer" }] } });

    const result = await listBuiltinPlugins();

    expect(mockedGet).toHaveBeenCalledWith("/api/core/plugins");
    expect(result).toEqual([{ id: "writer" }]);
  });

  it("returns an empty array when the plugins field is missing", async () => {
    mockedGet.mockResolvedValue({ data: {} });

    const result = await listBuiltinPlugins();

    expect(result).toEqual([]);
  });

  it("fetches a single builtin plugin by id", async () => {
    mockedGet.mockResolvedValue({ data: { id: "writer", name: "Writer" } });

    const result = await getBuiltinPlugin("writer");

    expect(mockedGet).toHaveBeenCalledWith("/api/core/plugins/writer");
    expect(result).toEqual({ id: "writer", name: "Writer" });
  });
});
