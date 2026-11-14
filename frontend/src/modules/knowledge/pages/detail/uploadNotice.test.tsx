import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { desktopTestTranslation as t, installDesktopTestDOM } from "@/test/desktopResourceFixtures";
const mocks = vi.hoisted(() => ({ availability: "ready", write: true, start: vi.fn(), load: vi.fn().mockResolvedValue(undefined) }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({}) } }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get: vi.fn().mockResolvedValue({ data: { ready: true } }) }, localizeErrorCode: (_code: string, fallback: string) => fallback }));
vi.mock("@/modules/knowledge/utils/polling", () => ({ default: class { start = mocks.start; cancel() {} } }));
vi.mock("@/modules/knowledge/utils/request", () => ({ TaskServiceApi: () => ({ listTasks: vi.fn() }), DocumentServiceApi: () => ({ documentServiceAllDocumentTags: vi.fn().mockResolvedValue({ data: { tags: [] } }) }) }));
vi.mock("@/modules/knowledge/store/dataset_permission", () => ({ useDatasetPermissionStore: (selector: (state: unknown) => unknown) => selector({ currentDataset: { dataset_id: "ds", display_name: "手册" }, loadDataset: mocks.load, hasOnlyReadPermission: () => false, hasUploadPermission: () => true, hasWritePermission: () => mocks.write }) }));
vi.mock("@/hooks/useModelFeatures", () => ({ fetchModelFeatures: vi.fn().mockResolvedValue({}), isImageEmbedRequired: () => false, MODEL_FEATURES_CHANGED_EVENT: "features" }));
vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({ useRAGComponent: () => ({ availability: mocks.availability }) }));
vi.mock("@/modules/user/uiPreferencesApi", () => ({ fetchUserUiPreferences: vi.fn().mockResolvedValue({ document_parsing_enabled: true }), USER_UI_PREFERENCES_CHANGED_EVENT: "preferences" }));
vi.mock("./components/KnowledgeTable", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("./components/ImportKnowledgeModal", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("./components/ImportTaskManage", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("./components/ImportTaskList", () => ({ TaskTab: {} }));
vi.mock("@/modules/knowledge/paper/PaperLibrary", () => ({ default: () => null }));
vi.mock("@/modules/knowledge/paper/pdfUpload", () => ({ createPaperPdfUploader: () => vi.fn() }));
vi.mock("@/modules/knowledge/components/KnowledgeBaseSyncNow", () => ({ default: () => null }));
vi.mock("@/components/ui", () => ({ DetailPageHeader: ({ title }: { title: React.ReactNode }) => <header>{title}</header> }));
import Detail from "./index";
afterEach(cleanup);
it("shows WAITING tasks as background queueing without an upload warning", async () => {
  installDesktopTestDOM();
  await act(async () => { render(<MemoryRouter initialEntries={["/ds"]}><Routes><Route path="/:id" element={<Detail />} /></Routes></MemoryRouter>); });
  const poll = mocks.start.mock.calls[mocks.start.mock.calls.length - 1]![0];
  await act(async () => poll.onSuccess({ data: { tasks: [{ task_id: "queued", task_state: "WAITING" }], total_size: 1 } }));
  expect(screen.queryByText(t("knowledge.documentUploadingKeepTabOpen"))).not.toBeInTheDocument();
  expect(screen.getByText(t("knowledge.documentsQueuedForParsing", { count: 1 }))).toBeVisible();
});

for (const availability of ["missing", "checking", "error"]) {
  it(`keeps local folder and task operations usable when RAG is ${availability}`, async () => {
    installDesktopTestDOM();
    mocks.availability = availability;
    mocks.write = true;
    await act(async () => { render(<MemoryRouter initialEntries={["/ds"]}><Routes><Route path="/:id" element={<Detail />} /></Routes></MemoryRouter>); });
    expect(screen.getByRole("button", { name: t("knowledge.createFolder") })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: `${t("knowledge.importFile")} down` }));
    expect(await screen.findByRole("menuitem", { name: t("knowledge.taskManageParse") })).not.toHaveAttribute("aria-disabled", "true");
    fireEvent.click(screen.getByRole("button", { name: `${t("knowledge.batchActions")} down` }));
    expect(await screen.findByRole("menuitem", { name: t("knowledge.batchMove") })).not.toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: t("knowledge.batchDelete") })).not.toHaveAttribute("aria-disabled", "true");
  });
}
it("does not grant folder or batch write operations to an upload-only user", async () => {
  installDesktopTestDOM();
  mocks.availability = "missing";
  mocks.write = false;
  await act(async () => { render(<MemoryRouter initialEntries={["/ds"]}><Routes><Route path="/:id" element={<Detail />} /></Routes></MemoryRouter>); });
  expect(screen.queryByRole("button", { name: t("knowledge.createFolder") })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: `${t("knowledge.batchActions")} down` })).not.toBeInTheDocument();
});
