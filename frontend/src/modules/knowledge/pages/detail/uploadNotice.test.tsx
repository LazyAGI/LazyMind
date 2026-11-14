import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { desktopTestTranslation as t, installDesktopTestDOM } from "@/test/desktopResourceFixtures";
const mocks = vi.hoisted(() => ({ start: vi.fn(), refresh: vi.fn(), batchMove: vi.fn(), readOnly: false, scene: "general", load: vi.fn().mockResolvedValue(undefined) }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({}) } }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get: vi.fn().mockResolvedValue({ data: { ready: true } }) }, localizeErrorCode: (_code: string, fallback: string) => fallback }));
vi.mock("@/modules/knowledge/utils/polling", () => ({ default: class { start = mocks.start; cancel() {} } }));
vi.mock("@/modules/knowledge/utils/request", () => ({ TaskServiceApi: () => ({ listTasks: vi.fn() }), DocumentServiceApi: () => ({ documentServiceAllDocumentTags: vi.fn().mockResolvedValue({ data: { tags: [] } }) }) }));
vi.mock("@/modules/knowledge/store/dataset_permission", () => ({ useDatasetPermissionStore: (selector: (state: unknown) => unknown) => selector({ currentDataset: { dataset_id: "ds", display_name: "手册", scene: mocks.scene }, loadDataset: mocks.load, hasOnlyReadPermission: () => mocks.readOnly, hasUploadPermission: () => !mocks.readOnly, hasWritePermission: () => !mocks.readOnly }) }));
vi.mock("@/hooks/useModelFeatures", () => ({ fetchModelFeatures: vi.fn().mockResolvedValue({}), isImageEmbedRequired: () => false, MODEL_FEATURES_CHANGED_EVENT: "features" }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({ useRAGComponent: () => ({ availability: "ready" }) }));
vi.mock("@/modules/user/uiPreferencesApi", () => ({ fetchUserUiPreferences: vi.fn().mockResolvedValue({ document_parsing_enabled: true }), USER_UI_PREFERENCES_CHANGED_EVENT: "preferences" }));
vi.mock("./components/KnowledgeTable", async () => {
  const { forwardRef, useImperativeHandle } = await import("react");
  return { default: forwardRef((_props, ref) => {
    useImperativeHandle(ref, () => ({ refresh: mocks.refresh, openBatchMove: mocks.batchMove }));
    return <div data-testid="document-table" />;
  }) };
});
vi.mock("./components/ImportKnowledgeModal", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("./components/ImportTaskManage", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("./components/ImportTaskList", () => ({ TaskTab: {} }));
vi.mock("@/modules/knowledge/paper/PaperLibrary", () => ({ default: () => <div data-testid="paper-library" /> }));
vi.mock("@/modules/knowledge/paper/pdfUpload", () => ({ createPaperPdfUploader: () => vi.fn() }));
vi.mock("@/modules/knowledge/components/KnowledgeBaseSyncNow", () => ({ default: () => null }));
vi.mock("@/components/ui", () => ({ DetailPageHeader: ({ title }: { title: React.ReactNode }) => <header>{title}</header> }));
import Detail from "./index";
afterEach(cleanup);
beforeEach(() => { mocks.readOnly = false; mocks.scene = "general"; vi.clearAllMocks(); });
async function renderDetail() {
  installDesktopTestDOM();
  await act(async () => { render(<MemoryRouter initialEntries={["/ds"]}><Routes><Route path="/:id" element={<Detail />} /></Routes></MemoryRouter>); });
}

it("keeps search and batch move connected to the document table", async () => {
  await renderDetail();
  const search = screen.getByRole("searchbox", { name: t("knowledge.searchDocPlaceholder") });
  fireEvent.change(search, { target: { value: "guide" } });
  fireEvent.keyDown(search, { key: "Enter", code: "Enter", charCode: 13, keyCode: 13 });
  expect(mocks.refresh).toHaveBeenCalledWith("guide");
  fireEvent.click(screen.getByRole("button", { name: new RegExp(t("knowledge.batchActions")) }));
  fireEvent.click(await screen.findByText(t("knowledge.batchMove")));
  expect(mocks.batchMove).toHaveBeenCalledOnce();
});

it("keeps read-only documents searchable without mutation controls", async () => {
  mocks.readOnly = true;
  await renderDetail();
  expect(screen.getByTestId("document-table")).toBeVisible();
  expect(screen.getByRole("searchbox", { name: t("knowledge.searchDocPlaceholder") })).toBeVisible();
  expect(screen.queryByRole("button", { name: t("knowledge.importFile") })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: t("knowledge.createFolder") })).not.toBeInTheDocument();
});

it("continues rendering paper collections with their own library", async () => {
  mocks.scene = "academic_papers";
  await renderDetail();
  expect(screen.getByTestId("paper-library")).toBeVisible();
  expect(screen.queryByTestId("document-table")).not.toBeInTheDocument();
});
it("shows WAITING tasks as background queueing without an upload warning", async () => {
  installDesktopTestDOM();
  await act(async () => { render(<MemoryRouter initialEntries={["/ds"]}><Routes><Route path="/:id" element={<Detail />} /></Routes></MemoryRouter>); });
  const poll = mocks.start.mock.calls.at(-1)![0];
  await act(async () => poll.onSuccess({ data: { tasks: [{ task_id: "queued", task_state: "WAITING" }], total_size: 1 } }));
  expect(screen.queryByText(t("knowledge.documentUploadingKeepTabOpen"))).not.toBeInTheDocument();
  expect(screen.getByText(t("knowledge.documentsQueuedForParsing", { count: 1 }))).toBeVisible();
});
