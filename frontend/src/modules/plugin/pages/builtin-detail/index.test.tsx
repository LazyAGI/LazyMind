import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, Outlet } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { testI18n } from "@/test/testUtils";
import BuiltinPluginDetailPage from "./index";
import { getBuiltinPlugin } from "../../pluginDraftApi";
import type { BuiltinPlugin } from "../../pluginDraftApi";

vi.mock("../../pluginDraftApi", () => ({
  getBuiltinPlugin: vi.fn(),
}));

vi.mock("../../components/StateGraphEditor", () => ({
  default: (props: { pluginName?: React.ReactNode; initialStateYaml?: string; readonly?: boolean }) => (
    <div data-testid="state-graph-editor">
      <div data-testid="editor-plugin-name">{props.pluginName}</div>
      <div data-testid="editor-initial-yaml">{props.initialStateYaml ?? ""}</div>
      <div data-testid="editor-readonly">{String(props.readonly)}</div>
    </div>
  ),
}));

const getBuiltinPluginMock = getBuiltinPlugin as ReturnType<typeof vi.fn>;

function makePlugin(overrides: Partial<BuiltinPlugin> = {}): BuiltinPlugin {
  return {
    id: "builtin-1",
    name: "Builtin Plugin",
    description: "",
    steps: [],
    ...overrides,
  };
}

function OutletContextLayout() {
  return <Outlet context={{ isMenuCollapsed: false, toggleMenu: vi.fn() }} />;
}

function renderPage(pluginId = "builtin-1") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[`/memory-management/plugins/builtin/${pluginId}`]}>
        <Routes>
          <Route element={<OutletContextLayout />}>
            <Route path="/memory-management/plugins/builtin/:pluginId" element={<BuiltinPluginDetailPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("BuiltinPluginDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows a skeleton while the builtin plugin is loading", () => {
    getBuiltinPluginMock.mockReturnValue(new Promise(() => {}));
    const { container } = renderPage();
    expect(container.querySelector(".ant-skeleton")).toBeInTheDocument();
  });

  it("shows an error alert with a back-to-list action when loading fails", async () => {
    getBuiltinPluginMock.mockRejectedValue(new Error("boom"));
    renderPage();
    await waitFor(() => {
      expect(document.querySelector(".ant-alert-error")).toBeInTheDocument();
    });
    expect(screen.getByText("selfEvolutionRun.builtinPluginBackToList")).toBeInTheDocument();
  });

  it("renders the readonly StateGraphEditor with the loaded plugin's yaml once ready", async () => {
    getBuiltinPluginMock.mockResolvedValue(
      makePlugin({ state_yaml_raw: "steps: {}", plugin_yaml_raw: 'name: "Builtin Plugin"\n' }),
    );
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("state-graph-editor")).toBeInTheDocument();
    });
    expect(screen.getByTestId("editor-initial-yaml")).toHaveTextContent("steps: {}");
    expect(screen.getByTestId("editor-readonly")).toHaveTextContent("true");
  });

  it("shows the breadcrumb with the plugin name once loaded", async () => {
    getBuiltinPluginMock.mockResolvedValue(makePlugin({ name: "My Builtin" }));
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("My Builtin")).toBeInTheDocument();
    });
    expect(screen.getByText("selfEvolutionRun.builtinPluginListBreadcrumb")).toBeInTheDocument();
  });
});
