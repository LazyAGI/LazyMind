import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import Detail from "./index";

const getDatasetMock = vi.fn();
const navigateMock = vi.fn();

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>(
    "react-router-dom",
  );
  return { ...actual, useNavigate: () => navigateMock };
});

vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceGetDataset: (...args: unknown[]) => getDatasetMock(...args),
    datasetServiceUpdateDataset: vi.fn().mockResolvedValue({}),
    datasetServiceDeleteDataset: vi.fn().mockResolvedValue({}),
  }),
  DocumentServiceApi: () => ({
    documentServiceCreateDocument: vi.fn().mockResolvedValue({}),
  }),
  TaskServiceApi: () => ({
    listTasks: vi.fn().mockResolvedValue({ data: { tasks: [], total_size: 0 } }),
    getTask: vi.fn().mockResolvedValue({ data: {} }),
  }),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: () => ({ role: "user" }),
  },
}));

vi.mock("@/hooks/useModelFeatures", () => ({
  fetchModelFeatures: vi.fn().mockResolvedValue({ image_embed_enabled: true, image_embed_required: false }),
  isImageEmbedRequired: (features: { image_embed_required?: boolean }) =>
    features.image_embed_required === true,
  MODEL_FEATURES_CHANGED_EVENT: "lazymind:model-features-changed",
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { get: vi.fn().mockResolvedValue({ data: { ready: true } }) },
  BASE_URL: "",
  localizeErrorCode: (msg: string) => msg,
}));

vi.mock("@/utils/developerMode", () => ({
  DEVELOPER_ACTIVE_EVENT: "lazymind:developer-active-change",
  isDeveloperModeActive: () => false,
}));

vi.mock("@/runtime/features", () => ({
  runtimeFeatures: { hideUserGroupSurfaces: false },
}));

// `@/components/ui`'s barrel file re-exports RenderPdf, which pulls in
// pdfjs-dist and crashes in jsdom (no DOMMatrix). This page only needs
// DetailPageHeader, so stub the barrel with a minimal implementation.
vi.mock("@/components/ui", () => ({
  DetailPageHeader: (props: {
    title?: ReactNode;
    onBack?: () => void;
    settingsMenu?: ReactNode;
  }) => (
    <div>
      <span>{props.title}</span>
      {props.settingsMenu}
      <button aria-label="back" onClick={() => props.onBack?.()}>
        back
      </button>
    </div>
  ),
}));

vi.mock("./components/KnowledgeTable", () => ({
  __esModule: true,
  default: () => <div data-testid="knowledge-table" />,
}));

vi.mock("./components/ImportKnowledgeModal", () => ({
  __esModule: true,
  default: () => <div data-testid="import-knowledge-modal" />,
}));

vi.mock("./components/ImportTaskManage", () => ({
  __esModule: true,
  default: () => <div data-testid="import-task-manage" />,
}));

vi.mock("@/modules/knowledge/components/RenameModel", () => ({
  __esModule: true,
  default: () => <div data-testid="rename-model" />,
}));

vi.mock("@/modules/knowledge/components/UpdateModal", () => ({
  __esModule: true,
  default: () => <div data-testid="update-modal" />,
}));

vi.mock("@/components/ui/TypedConfirmModal", () => ({
  __esModule: true,
  default: () => <div data-testid="typed-confirm-modal" />,
}));

vi.mock("@/modules/knowledge/components/KnowledgeBaseSyncNow", () => ({
  __esModule: true,
  default: () => <div data-testid="knowledge-base-sync-now" />,
}));

function renderDetail(initialPath = "/lib/knowledge/1") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/lib/knowledge/:id" element={<Detail />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("Detail page", () => {
  beforeEach(() => {
    getDatasetMock.mockReset().mockResolvedValue({
      data: {
        dataset_id: "1",
        display_name: "My KB",
        acl: ["dataset:write", "dataset:upload"],
        tags: ["tag-a"],
      },
    });
    navigateMock.mockReset();
  });

  it("fetches the dataset detail and renders the title", async () => {
    renderDetail();

    await waitFor(() => {
      expect(getDatasetMock).toHaveBeenCalledWith({ dataset: "1" });
    });
    await waitFor(() => {
      expect(screen.getByText("My KB")).toBeInTheDocument();
    });
  });

  it("renders the knowledge table once the detail has loaded", async () => {
    renderDetail();

    await waitFor(() => {
      expect(screen.getByTestId("knowledge-table")).toBeInTheDocument();
    });
  });

  it("navigates back when the header back button is clicked", async () => {
    renderDetail();

    await waitFor(() => {
      expect(screen.getByTestId("knowledge-table")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "back" }));

    expect(navigateMock).toHaveBeenCalledWith(-1);
  });

  it("navigates to knowledge list instead when coming from chat/aiwrite/aireview", async () => {
    renderDetail("/lib/knowledge/1?from=chat");

    await waitFor(() => {
      expect(screen.getByTestId("knowledge-table")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "back" }));

    expect(navigateMock).toHaveBeenCalledWith("/lib/knowledge/list");
  });

  it("renders the import/edit sub-modals", async () => {
    renderDetail();

    await waitFor(() => {
      expect(screen.getByTestId("import-knowledge-modal")).toBeInTheDocument();
    });
    expect(screen.getByTestId("import-task-manage")).toBeInTheDocument();
    expect(screen.getByTestId("rename-model")).toBeInTheDocument();
    expect(screen.getByTestId("update-modal")).toBeInTheDocument();
    expect(screen.getByTestId("typed-confirm-modal")).toBeInTheDocument();
  });
});
