import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import AppRouter from "./index";

function stub(testId: string) {
  return { default: () => <div data-testid={testId} /> };
}

vi.mock("@/layouts/MainLayout", () => ({
  default: () => (
    <div data-testid="main-layout">
      <div data-testid="outlet-slot" />
    </div>
  ),
}));
vi.mock("@/modules/signin/pages/login", () => stub("signin-login"));
vi.mock("@/modules/signin/pages/register", () => stub("signin-register"));
vi.mock("@/modules/signin/pages/dashboard", async () => {
  const { Outlet } = await import("react-router-dom");
  return {
    default: () => (
      <div data-testid="signin-dashboard">
        <Outlet />
      </div>
    ),
  };
});
vi.mock("@/modules/signin/pages/loginTransition", () => stub("login-transition"));
vi.mock("@/modules/chat/ChatApp", () => stub("chat-app"));
vi.mock("@/modules/chat/pages/home", () => stub("chat-home"));
vi.mock("@/modules/knowledge/KnowledgeApp", () => stub("knowledge-app"));
vi.mock("@/modules/knowledge/pages/list", () => stub("knowledge-list"));
vi.mock("@/modules/knowledge/pages/auth", () => stub("knowledge-auth"));
vi.mock("@/modules/knowledge/pages/detail", () => stub("knowledge-detail"));
vi.mock("@/modules/knowledge/pages/knowledge", () => stub("knowledge-knowledge"));
vi.mock("@/modules/admin/AdminLayout", () => stub("admin-layout"));
vi.mock("@/modules/taskCenter", () => stub("task-center"));
vi.mock("@/modules/admin/pages/user", () => stub("admin-users"));
vi.mock("@/modules/admin/pages/group", () => stub("admin-groups"));
vi.mock("@/modules/admin/pages/group/detail.tsx", () => stub("admin-group-detail"));
vi.mock("@/modules/dataSource/database", () => stub("database-connections"));
vi.mock("@/modules/dataSource/common/feishuCallback", () => stub("feishu-callback"));
vi.mock("@/modules/modelProvider/pages/CloudDocumentsPage", () => stub("cloud-documents"));
vi.mock("@/modules/modelProvider/pages/FeishuAccountPage", () => stub("feishu-account"));
vi.mock("@/modules/modelProvider/pages/GoogleDriveConnectionPage", () => stub("google-drive-connection"));
vi.mock("@/modules/modelProvider/pages/GoogleDriveSetupGuide", () => stub("google-drive-setup"));
vi.mock("@/modules/modelProvider/pages/LocalDataSourcePage", () => stub("local-data-source"));
vi.mock("@/modules/modelProvider/pages/FeishuSetupGuide", () => stub("feishu-setup"));
vi.mock("@/modules/modelProvider/pages/NotionSetupGuide", () => stub("notion-setup"));
vi.mock("@/modules/datasetManagement/pages/list", () => stub("dataset-list"));
vi.mock("@/modules/datasetManagement/pages/detail", () => stub("dataset-detail"));
vi.mock("@/modules/channelGateway", () => ({
  TerminalConnectionPage: () => <div data-testid="channel-connection" />,
}));
vi.mock("@/modules/memory", () => stub("memory-management"));
vi.mock("@/modules/memory/pages/list", () => stub("memory-list"));
vi.mock("@/modules/memory/pages/review", () => stub("memory-review"));
vi.mock("@/modules/memory/pages/glossaryDetail", () => stub("memory-glossary-detail"));
vi.mock("@/modules/memory/pages/skillDetail", () => stub("memory-skill-detail"));
vi.mock("@/modules/memory/pages/experienceDetail", () => stub("memory-experience-detail"));
vi.mock("@/modules/modelProvider", () => stub("model-provider"));
vi.mock("@/modules/modelProvider/CloudDocumentsLayout", () => stub("cloud-documents-layout"));
vi.mock("@/modules/modelProvider/pages/ModelProvidersPage", () => stub("model-providers-page"));
vi.mock("@/modules/modelProvider/pages/ExternalServicesPage", () => stub("external-services"));
vi.mock("@/modules/modelProvider/pages/DefaultServicesPage", () => stub("default-services"));
vi.mock("@/modules/selfEvolution", () => ({
  SelfEvolutionAlgorithmManagementPage: () => <div data-testid="evo-algorithms" />,
  SelfEvolutionRoutingStrategyPage: () => <div data-testid="evo-routing" />,
  SelfEvolutionHomePage: () => <div data-testid="evo-home" />,
  SelfEvolutionDetailPage: () => <div data-testid="evo-detail" />,
  SelfEvolutionObservationPage: () => <div data-testid="evo-observation" />,
}));
vi.mock("@/pages/UserAgreementPage", () => stub("user-agreement"));
vi.mock("@/modules/plugin/pages/detail", () => stub("plugin-detail"));
vi.mock("@/modules/plugin/pages/builtin-detail", () => stub("builtin-plugin-detail"));

vi.mock("@/i18n/antdLocale", () => ({
  getAntdLocale: () => undefined,
}));

const isLocalSessionEnabledMock = vi.hoisted(() => vi.fn());
vi.mock("@/runtime/localSession", () => ({
  isLocalSessionEnabled: isLocalSessionEnabledMock,
}));

const runtimeFeaturesMock = vi.hoisted(() => ({
  hideRegister: false,
  hideCloudAdmin: false,
  hideEvo: true,
  hideUserGroupSurfaces: false,
}));
vi.mock("@/runtime/features", () => ({
  runtimeFeatures: runtimeFeaturesMock,
}));

function renderAt(path: string) {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[path]}>
        <AppRouter />
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("AppRouter", () => {
  beforeEach(() => {
    isLocalSessionEnabledMock.mockReset().mockReturnValue(false);
    runtimeFeaturesMock.hideRegister = false;
    runtimeFeaturesMock.hideCloudAdmin = false;
    runtimeFeaturesMock.hideEvo = true;
    runtimeFeaturesMock.hideUserGroupSurfaces = false;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the login page for /login when local session is disabled", () => {
    renderAt("/login");
    expect(screen.getByTestId("signin-dashboard")).toBeInTheDocument();
    expect(screen.getByTestId("signin-login")).toBeInTheDocument();
  });

  it("redirects /login to /agent/chat when local session is enabled", () => {
    isLocalSessionEnabledMock.mockReturnValue(true);
    renderAt("/login");
    expect(screen.getByTestId("main-layout")).toBeInTheDocument();
  });

  it("renders the register page when registration is not hidden", () => {
    renderAt("/register");
    expect(screen.getByTestId("signin-register")).toBeInTheDocument();
  });

  it("redirects /register to /login when registration is hidden", () => {
    runtimeFeaturesMock.hideRegister = true;
    renderAt("/register");
    expect(screen.getByTestId("signin-dashboard")).toBeInTheDocument();
    expect(screen.getByTestId("signin-login")).toBeInTheDocument();
  });

  it("renders the standalone user agreement page", () => {
    renderAt("/legal/user-agreement");
    expect(screen.getByTestId("user-agreement")).toBeInTheDocument();
  });

  it("renders the main layout for the root path", () => {
    renderAt("/");
    expect(screen.getByTestId("main-layout")).toBeInTheDocument();
  });

  it("renders the admin layout for /admin/groups when cloud admin is not hidden", () => {
    renderAt("/admin/groups");
    expect(screen.getByTestId("admin-layout")).toBeInTheDocument();
  });

  it("redirects /admin/* to /agent/chat when cloud admin is hidden", () => {
    runtimeFeaturesMock.hideCloudAdmin = true;
    renderAt("/admin/groups");
    expect(screen.getByTestId("main-layout")).toBeInTheDocument();
  });

  it("redirects unknown paths to the root", () => {
    renderAt("/some/unknown/path");
    expect(screen.getByTestId("main-layout")).toBeInTheDocument();
  });
});
