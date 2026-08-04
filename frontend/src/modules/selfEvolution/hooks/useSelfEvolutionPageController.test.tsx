import { describe, expect, it, vi, beforeEach } from "vitest";

// jsdom does not implement URL.createObjectURL, which the controller uses to
// build a downloadable blob URL for AB-test category comparisons.
if (!URL.createObjectURL) {
  URL.createObjectURL = vi.fn(() => "blob:mock-url");
}

const { axiosGet, axiosPost, datasetServiceListDatasets } = vi.hoisted(() => ({
  axiosGet: vi.fn(),
  axiosPost: vi.fn(),
  datasetServiceListDatasets: vi.fn(),
}));

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: axiosGet, post: axiosPost },
  localizeErrorCode: (_code?: string, fallback = "") => fallback,
  extractErrorCode: () => undefined,
}));

vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets,
  }),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getAuthHeaders: () => ({}),
    refreshAccessToken: vi.fn(),
  },
}));

vi.mock("@/modules/knowledge/components/MarkdownViewer", () => ({
  default: ({ children }: { children: string }) => <div data-testid="markdown-viewer">{children}</div>,
}));

vi.mock("@/modules/chat/assets/icons/send_icon.svg?react", () => ({
  default: () => <svg data-testid="send-icon" />,
}));

import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import {
  SelfEvolutionPageController,
  type SelfEvolutionPageRenderProps,
} from "./useSelfEvolutionPageController";

function makeWrapper(initialPath: string, view: "home" | "detail") {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <I18nextProvider i18n={testI18n}>
        <MemoryRouter initialEntries={[initialPath]}>
          <Routes>
            <Route
              path={view === "detail" ? "/self-evolution/detail/:threadId?" : "/self-evolution"}
              element={<>{children}</>}
            />
          </Routes>
        </MemoryRouter>
      </I18nextProvider>
    );
  };
}

// Captures the latest render props emitted by SelfEvolutionPageController's
// render-prop children function, since renderHook cannot directly render a
// component that takes a render-prop child.
function renderController(view: "home" | "detail", initialPath: string) {
  let captured: SelfEvolutionPageRenderProps | undefined;
  const { result, rerender } = renderHook(
    () => {
      let props: SelfEvolutionPageRenderProps | undefined;
      SelfEvolutionPageController({
        view,
        children: (renderProps) => {
          props = renderProps;
          captured = renderProps;
          return null;
        },
      });
      return props;
    },
    { wrapper: makeWrapper(initialPath, view) },
  );
  return { result, rerender, getProps: () => captured };
}

beforeEach(() => {
  window.localStorage.clear();
  axiosGet.mockReset().mockResolvedValue({ data: {} });
  axiosPost.mockReset().mockResolvedValue({ data: {} });
  datasetServiceListDatasets.mockReset().mockResolvedValue({
    data: { datasets: [{ dataset_id: "kb-1", display_name: "KB One" }] },
  });
});

describe("SelfEvolutionPageController", () => {
  it("starts on the home view (workbench hidden) when view is home", async () => {
    const { getProps } = renderController("home", "/self-evolution");
    await waitFor(() => {
      expect(getProps()?.isWorkbenchVisible).toBe(false);
    });
    expect(getProps()?.homeViewProps).toBeDefined();
    expect(getProps()?.workbenchViewProps).toBeDefined();
  });

  it("shows the workbench immediately when view is detail", async () => {
    const { getProps } = renderController("detail", "/self-evolution/detail/thread-1");
    await waitFor(() => {
      expect(getProps()?.isWorkbenchVisible).toBe(true);
    });
  });

  it("loads knowledge base options on mount and exposes them via launch option cards", async () => {
    const { getProps } = renderController("home", "/self-evolution");
    await waitFor(() => {
      expect(datasetServiceListDatasets).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(getProps()?.homeViewProps.isLoadingThreadHistoryList).toBe(false);
    });
    expect(getProps()?.homeViewProps.launchOptionCards.length).toBeGreaterThan(0);
  });

  it("opens the history session modal and fetches thread history", async () => {
    axiosGet.mockResolvedValue({ data: { threads: [] } });
    const { result, getProps } = renderController("home", "/self-evolution");
    await waitFor(() => {
      expect(getProps()).toBeDefined();
    });

    act(() => {
      getProps()!.homeViewProps.onOpenHistorySessionModal();
    });

    await waitFor(() => {
      expect(getProps()?.homeHistoryModalProps.open).toBe(true);
    });
    await waitFor(() => {
      expect(axiosGet).toHaveBeenCalledWith(
        expect.stringContaining("/threads"),
        expect.objectContaining({ params: { page_size: 50 } }),
      );
    });
  });

  it("restores a thread's detail state when navigated directly to a thread id", async () => {
    axiosGet.mockImplementation((url: string) => {
      if (url.includes("/steps")) {
        return Promise.resolve({
          data: {
            steps: [{ step_id: "dataset", status: "running", active: true }],
          },
        });
      }
      if (url.includes("/messages")) {
        return Promise.resolve({ data: { items: [] } });
      }
      if (url.endsWith("/threads/thread-42")) {
        return Promise.resolve({ data: { thread: { status: { state: "ended" } } } });
      }
      return Promise.resolve({ data: {} });
    });

    const { getProps } = renderController("detail", "/self-evolution/detail/thread-42");

    await waitFor(() => {
      expect(getProps()?.workbenchViewProps.isRestoringThread).toBe(false);
    });
    expect(getProps()?.workbenchViewProps.routeThreadId).toBe("thread-42");
  });
});
