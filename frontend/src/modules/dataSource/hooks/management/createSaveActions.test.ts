import { act } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";

const getSourceMock = vi.fn();
const updateSourceMock = vi.fn();
const createSourceMock = vi.fn();
const triggerSourceSyncMock = vi.fn();

vi.mock("../../api/clients", () => ({
  dataSourceScanApi: {
    getSource: (...args: unknown[]) => getSourceMock(...args),
    updateSource: (...args: unknown[]) => updateSourceMock(...args),
    createSource: (...args: unknown[]) => createSourceMock(...args),
    triggerSourceSync: (...args: unknown[]) => triggerSourceSyncMock(...args),
  },
}));

vi.mock("../../utils/cloudSync", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../utils/cloudSync")>();
  return { ...actual, waitForCloudSyncRun: vi.fn().mockResolvedValue(undefined) };
});

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: unknown) => `localized:${(error as Error)?.message || error}`,
  localizeErrorCode: (code?: string) => `localized-code:${code}`,
}));

import { createSaveActions } from "./createSaveActions";
import type { ManagementContext } from "./context";

const t = ((key: string) => key) as TFunction;

function makeForm(values: Record<string, unknown>) {
  return {
    validateFields: vi.fn().mockResolvedValue(undefined),
    getFieldsValue: vi.fn(() => values),
    setFields: vi.fn(),
    scrollToField: vi.fn(),
  };
}

function makeContext(overrides: Partial<ManagementContext> = {}): ManagementContext {
  const base = {
    t,
    form: makeForm({}),
    scanAgents: [],
    editingId: null,
    wizardMode: "create",
    selectedType: "local",
    sources: [],
    validatedAgentId: null,
    setValidatedAgentId: vi.fn(),
    refreshSources: vi.fn().mockResolvedValue(undefined),
    handleCloseWizard: vi.fn(),
    canCreateLocalSource: true,
    createSuccessMessageKey: undefined,
    setWizardSaving: vi.fn(),
    setWizardSavingMode: vi.fn(),
    wizardSaving: false,
    oauthConnection: null,
    feishuTargetTreeData: [],
    ...overrides,
  };
  return base as unknown as ManagementContext;
}

describe("createSaveActions", () => {
  beforeEach(() => {
    getSourceMock.mockReset();
    updateSourceMock.mockReset();
    createSourceMock.mockReset();
    triggerSourceSyncMock.mockReset();
  });

  it("warns and skips saving when no local path is provided", async () => {
    const messageModule = await import("antd");
    const warnSpy = vi.spyOn(messageModule.message, "warning").mockImplementation(() => "" as any);

    const ctx = makeContext({
      form: makeForm({ path: [], knowledgeBase: "KB" }),
    });
    const { handleSave } = createSaveActions(ctx);

    await act(async () => { await handleSave("create"); });

    expect(warnSpy).toHaveBeenCalledWith("admin.dataSourceAccessPathRequired");
    expect(createSourceMock).not.toHaveBeenCalled();
    warnSpy.mockRestore();
  });

  it("creates a local source with the submitted path and refreshes sources", async () => {
    createSourceMock.mockResolvedValue({
      data: { source: { source_id: "src-1", dataset_id: "ds-1" } },
    });

    const refreshSources = vi.fn().mockResolvedValue(undefined);
    const handleCloseWizard = vi.fn();
    const ctx = makeContext({
      form: makeForm({ path: ["/data"], knowledgeBase: "KB", fileTypes: ["pdf"] }),
      refreshSources,
      handleCloseWizard,
    });
    const { handleSave } = createSaveActions(ctx);

    await act(async () => { await handleSave("create"); });

    expect(createSourceMock).toHaveBeenCalledWith(
      expect.objectContaining({
        createSourceRequest: expect.objectContaining({
          name: "KB",
          source_options: expect.objectContaining({ source_type: "local" }),
        }),
      }),
    );
    expect(triggerSourceSyncMock).not.toHaveBeenCalled();
    expect(refreshSources).toHaveBeenCalledWith(false);
    expect(handleCloseWizard).toHaveBeenCalled();
  });

  it("triggers a sync after create when saveMode is createAndSync", async () => {
    createSourceMock.mockResolvedValue({
      data: { source: { source_id: "src-1", dataset_id: "ds-1" } },
    });
    triggerSourceSyncMock.mockResolvedValue({ data: {} });

    const ctx = makeContext({
      form: makeForm({ path: ["/data"], knowledgeBase: "KB" }),
    });
    const { handleSave } = createSaveActions(ctx);

    await act(async () => { await handleSave("createAndSync"); });

    expect(triggerSourceSyncMock).toHaveBeenCalledWith(
      expect.objectContaining({ sourceId: "src-1" }),
    );
  });

  it("updates an existing local source when editing", async () => {
    getSourceMock.mockResolvedValue({
      data: { bindings: [], source: { config_version: 3 } },
    });
    updateSourceMock.mockResolvedValue({ data: {} });

    const ctx = makeContext({
      wizardMode: "edit",
      editingId: "src-1",
      form: makeForm({ path: ["/data"], knowledgeBase: "KB" }),
    });
    const { handleSave } = createSaveActions(ctx);

    await act(async () => { await handleSave("create"); });

    expect(updateSourceMock).toHaveBeenCalledWith(
      expect.objectContaining({
        sourceId: "src-1",
        updateSourceRequest: expect.objectContaining({
          config_version: 3,
          name: "KB",
        }),
      }),
    );
  });

  it("marks the knowledge base field as duplicated when the API reports a name conflict", async () => {
    createSourceMock.mockRejectedValue({
      isAxiosError: true,
      response: { data: { code: "2001102" } },
    });

    const form = makeForm({ path: ["/data"], knowledgeBase: "KB" });
    const ctx = makeContext({ form });
    const { handleSave } = createSaveActions(ctx);

    await act(async () => { await handleSave("create"); });

    expect(form.setFields).toHaveBeenCalledWith([
      expect.objectContaining({ name: "knowledgeBase" }),
    ]);
    expect(form.scrollToField).toHaveBeenCalledWith("knowledgeBase", { block: "center" });
  });

  it("warns and skips saving when the selected source type cannot be resolved", async () => {
    const messageModule = await import("antd");
    const warnSpy = vi.spyOn(messageModule.message, "warning").mockImplementation(() => "" as any);

    const ctx = makeContext({
      selectedType: null,
      form: makeForm({}),
    });
    const { handleSave } = createSaveActions(ctx);

    await act(async () => { await handleSave("create"); });

    expect(warnSpy).toHaveBeenCalledWith("admin.dataSourceSelectTypeFirst");
    expect(createSourceMock).not.toHaveBeenCalled();
    warnSpy.mockRestore();
  });
});
