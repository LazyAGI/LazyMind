import { lazy, Suspense } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { ConfigProvider, Spin } from "antd";
import { useTranslation } from "react-i18next";
import MainLayout from "@/layouts/MainLayout";
import SigninLogin from "@/modules/signin/pages/login";
import SigninRegister from "@/modules/signin/pages/register";
import SigninDashboard from "@/modules/signin/pages/dashboard";
import LoginTransition from "@/modules/signin/pages/loginTransition";
import ChatApp from "@/modules/chat/ChatApp";
import Home from "@/modules/chat/pages/home";
import { getAntdLocale } from "@/i18n/antdLocale";
import { runtimeFeatures } from "@/runtime/features";
import { isLocalSessionEnabled } from "@/runtime/localSession";
import UserAgreementPage from "@/pages/UserAgreementPage";
import { isDesktopRuntime } from "@/runtime/mode";

const ShowcaseGalleryPage = lazy(() => import("@/modules/showcase/GalleryPage"));
const ShowcaseDetailPage = lazy(() => import("@/modules/showcase/DetailPage"));
const KnowledgeApp = lazy(() => import("@/modules/knowledge/KnowledgeApp"));
const KnowledgeList = lazy(() => import("@/modules/knowledge/pages/list"));
const KnowledgeAuth = lazy(() => import("@/modules/knowledge/pages/auth"));
const KnowledgeDetail = lazy(() => import("@/modules/knowledge/pages/detail"));
const Knowledge = lazy(() => import("@/modules/knowledge/pages/knowledge"));
const AdminLayout = lazy(() => import("@/modules/admin/AdminLayout"));
const TaskCenterPage = lazy(() => import("@/modules/taskCenter"));
const UserManagement = lazy(() => import("@/modules/admin/pages/user"));
const GroupManagement = lazy(() => import("@/modules/admin/pages/group"));
const GroupDetail = lazy(() => import("@/modules/admin/pages/group/detail.tsx"));
const DatabaseConnectionsPage = lazy(() => import("@/modules/dataSource/database"));
const DataSourceFeishuCallback = lazy(() => import("@/modules/dataSource/common/feishuCallback"));
const CloudDocumentsPage = lazy(() => import("@/modules/modelProvider/pages/CloudDocumentsPage"));
const FeishuAccountPage = lazy(() => import("@/modules/modelProvider/pages/FeishuAccountPage"));
const GoogleDriveConnectionPage = lazy(() => import("@/modules/modelProvider/pages/GoogleDriveConnectionPage"));
const GoogleDriveSetupGuide = lazy(() => import("@/modules/modelProvider/pages/GoogleDriveSetupGuide"));
const LocalDataSourcePage = lazy(() => import("@/modules/modelProvider/pages/LocalDataSourcePage"));
const FeishuSetupGuide = lazy(() => import("@/modules/modelProvider/pages/FeishuSetupGuide"));
const NotionSetupGuide = lazy(() => import("@/modules/modelProvider/pages/NotionSetupGuide"));
const DatasetListPage = lazy(() => import("@/modules/datasetManagement/pages/list"));
const DatasetDetailPage = lazy(() => import("@/modules/datasetManagement/pages/detail"));
const TerminalConnectionPage = lazy(() => import("@/modules/channelGateway").then((module) => ({
  default: module.TerminalConnectionPage,
})));
const MemoryManagement = lazy(() => import("@/modules/memory"));
const MemoryManagementListPage = lazy(() => import("@/modules/memory/pages/list"));
const MemoryReviewPage = lazy(() => import("@/modules/memory/pages/review"));
const MemoryGlossaryDetailPage = lazy(() => import("@/modules/memory/pages/glossaryDetail"));
const MemorySkillDetailPage = lazy(() => import("@/modules/memory/pages/skillDetail"));
const ModelProviderPage = lazy(() => import("@/modules/modelProvider"));
const CloudDocumentsLayout = lazy(() => import("@/modules/modelProvider/CloudDocumentsLayout"));
const ModelProvidersPage = lazy(() => import("@/modules/modelProvider/pages/ModelProvidersPage"));
const ExternalServicesPage = lazy(() => import("@/modules/modelProvider/pages/ExternalServicesPage"));
const DefaultServicesPage = lazy(() => import("@/modules/modelProvider/pages/DefaultServicesPage"));
const SelfEvolutionAlgorithmManagementPage = lazy(() => import("@/modules/selfEvolution").then((module) => ({
  default: module.SelfEvolutionAlgorithmManagementPage,
})));
const SelfEvolutionRoutingStrategyPage = lazy(() => import("@/modules/selfEvolution").then((module) => ({
  default: module.SelfEvolutionRoutingStrategyPage,
})));
const SelfEvolutionTrafficStatsPage = lazy(() => import("@/modules/selfEvolution").then((module) => ({
  default: module.SelfEvolutionTrafficStatsPage,
})));
const SelfEvolutionHomePage = lazy(() => import("@/modules/selfEvolution").then((module) => ({
  default: module.SelfEvolutionHomePage,
})));
const SelfEvolutionDetailPage = lazy(() => import("@/modules/selfEvolution").then((module) => ({
  default: module.SelfEvolutionDetailPage,
})));
const SelfEvolutionObservationPage = lazy(() => import("@/modules/selfEvolution").then((module) => ({
  default: module.SelfEvolutionObservationPage,
})));
const AgentIntegrationPage = lazy(() => import("@/modules/agentIntegration/AgentIntegrationPage"));
const WorkflowDetailPage = lazy(() => import("@/modules/workflow/pages/detail"));
const BuiltinWorkflowDetailPage = lazy(() => import("@/modules/workflow/pages/builtin-detail"));

export default function AppRouter() {
  const { i18n } = useTranslation();
  const localSessionEnabled = isLocalSessionEnabled();

  return (
    <ConfigProvider
      locale={getAntdLocale(i18n.resolvedLanguage || i18n.language)}
    >
      <Suspense fallback={<Spin style={{ display: "flex", justifyContent: "center", alignItems: "center", height: "100%" }} />}>
      <Routes>
        <Route
          path="/legal/user-agreement"
          element={<UserAgreementPage />}
        />
        {localSessionEnabled ? (
          <Route path="/login" element={<Navigate to="/agent/chat" replace />} />
        ) : (
          <Route path="/login" element={<SigninDashboard />}>
            <Route index element={<SigninLogin />} />
          </Route>
        )}
        {runtimeFeatures.hideRegister ? (
          <Route
            path="/register"
            element={
              <Navigate
                to={localSessionEnabled ? "/agent/chat" : "/login"}
                replace
              />
            }
          />
        ) : (
          <Route path="/register" element={<SigninDashboard />}>
            <Route index element={<SigninRegister />} />
          </Route>
        )}
        <Route
          path="/oauth/feishu/callback"
          element={<DataSourceFeishuCallback />}
        />
        <Route
          path="/oauth/notion/data-source/callback"
          element={<DataSourceFeishuCallback provider="notion" />}
        />
        <Route
          path="/oauth/notion/callback"
          element={<DataSourceFeishuCallback provider="notion" />}
        />
        <Route
          path="/oauth/googledrive/data-source/callback"
          element={<DataSourceFeishuCallback provider="googledrive" />}
        />
        <Route
          path="/oauth/googledrive/callback"
          element={<DataSourceFeishuCallback provider="googledrive" />}
        />
        <Route
          path="/loginTransition"
          element={
            localSessionEnabled ? (
              <Navigate to="/agent/chat" replace />
            ) : (
              <LoginTransition />
            )
          }
        />
        <Route path="/" element={<MainLayout />}>
          <Route index element={<Navigate to="/agent/chat" replace />} />
          <Route path="agent/chat" element={<ChatApp />}>
            <Route index element={<Navigate to="home" replace />} />
            <Route path="home" element={<Home />} />
            <Route path="cases" element={<ShowcaseGalleryPage />} />
            <Route path="cases/:caseId" element={<ShowcaseDetailPage />} />
          </Route>
          <Route path="lib/knowledge" element={<KnowledgeApp />}>
            <Route index element={<Navigate to="list" replace />} />
            <Route path="list" element={<KnowledgeList />} />
            {runtimeFeatures.hideUserGroupSurfaces ? (
              <Route
                path="auth/:id"
                element={<Navigate to="/lib/knowledge/list" replace />}
              />
            ) : (
              <Route path="auth/:id" element={<KnowledgeAuth />} />
            )}
            <Route path="detail/:id" element={<KnowledgeDetail />} />
            <Route
              path="knowledge/:knowledgeBaseId/:knowledgeId"
              element={<Knowledge />}
            />
          </Route>
          <Route path="dataset-management" element={<DatasetListPage />} />
          <Route
            path="dataset-management/:datasetId"
            element={<DatasetDetailPage />}
          />
          <Route path="databases" element={<DatabaseConnectionsPage />} />
          <Route path="channels" element={<TerminalConnectionPage />} />
          <Route
            path="channels/wechat"
            element={<Navigate to="/channels?provider=wechat" replace />}
          />
          <Route
            path="channels/feishu"
            element={<Navigate to="/channels?provider=feishu" replace />}
          />
          <Route path="cloud-documents" element={<CloudDocumentsLayout />}>
            <Route index element={<CloudDocumentsPage />} />
            <Route path="local" element={<LocalDataSourcePage />} />
            <Route path="feishu" element={<FeishuAccountPage />} />
            <Route path="google-drive" element={<GoogleDriveConnectionPage />} />
            <Route path="docs/feishu-setup" element={<FeishuSetupGuide />} />
            <Route path="docs/notion-setup" element={<NotionSetupGuide />} />
            <Route path="docs/google-drive-setup" element={<GoogleDriveSetupGuide />} />
          </Route>
          <Route path="model-providers" element={<ModelProviderPage />}>
            <Route index element={<Navigate to="default-services" replace />} />
            <Route path="models" element={<ModelProvidersPage />} />
            <Route
              path="document-parsing"
              element={<Navigate to="/model-providers/tools" replace />}
            />
            <Route path="tools" element={<ExternalServicesPage />} />
            <Route
              path="cloud-documents"
              element={<Navigate to="/cloud-documents" replace />}
            />
            <Route
              path="cloud-documents/local"
              element={<Navigate to="/cloud-documents/local" replace />}
            />
            <Route
              path="cloud-documents/feishu"
              element={<Navigate to="/cloud-documents/feishu" replace />}
            />
            <Route
              path="cloud-documents/google-drive"
              element={<Navigate to="/cloud-documents/google-drive" replace />}
            />
            <Route
              path="cloud-documents/docs/feishu-setup"
              element={<Navigate to="/cloud-documents/docs/feishu-setup" replace />}
            />
            <Route
              path="cloud-documents/docs/notion-setup"
              element={<Navigate to="/cloud-documents/docs/notion-setup" replace />}
            />
            <Route
              path="cloud-documents/docs/google-drive-setup"
              element={<Navigate to="/cloud-documents/docs/google-drive-setup" replace />}
            />
            <Route
              path="external-services"
              element={<Navigate to="/model-providers/tools" replace />}
            />
            <Route path="default-services" element={<DefaultServicesPage />} />
          </Route>
          <Route path="memory-management" element={<MemoryManagement />}>
            <Route index element={<MemoryManagementListPage />} />
            <Route
              path="tools"
              element={<Navigate to="/model-providers/tools" replace />}
            />
            <Route path="skills" element={<MemoryManagementListPage />} />
            <Route path="skills/:itemId" element={<MemorySkillDetailPage />} />
            <Route path="experience" element={<MemoryManagementListPage />} />
            <Route
              path="experience/:itemId"
              element={
                <Navigate to="/memory-management/experience" replace />
              }
            />
            <Route path="glossary" element={<MemoryManagementListPage />} />
            <Route
              path="glossary/:itemId"
              element={<MemoryGlossaryDetailPage />}
            />
            <Route
              path="review/experience/:itemId"
              element={
                <Navigate to="/memory-management/experience" replace />
              }
            />
            <Route path="review/:tab/:itemId" element={<MemoryReviewPage />} />
          </Route>
          <Route path="memory-management/workflows" element={<Navigate to="/memory-management/skills?skillView=workflows" replace />} />
          <Route path="memory-management/workflows/builtin/:workflowId" element={<BuiltinWorkflowDetailPage />} />
          <Route path="memory-management/workflows/:workflowId" element={<WorkflowDetailPage />} />
          {runtimeFeatures.hideEvo ? (
            <Route
              path="self-evolution/*"
              element={<Navigate to="/agent/chat" replace />}
            />
          ) : (
            <>
              <Route
                path="self-evolution"
                element={<SelfEvolutionHomePage />}
              />
              <Route
                path="self-evolution/algorithms"
                element={<SelfEvolutionAlgorithmManagementPage />}
              />
              <Route
                path="self-evolution/algorithms/routing-strategy"
                element={<SelfEvolutionRoutingStrategyPage />}
              />
              <Route
                path="self-evolution/algorithms/traffic-stats"
                element={<SelfEvolutionTrafficStatsPage />}
              />
              <Route
                path="self-evolution/detail/:threadId/observation/:kind"
                element={<SelfEvolutionObservationPage />}
              />
              <Route
                path="self-evolution/detail/:threadId"
                element={<SelfEvolutionDetailPage />}
              />
              <Route
                path="self-evolution/:threadId/observation/:kind"
                element={<SelfEvolutionObservationPage />}
              />
              <Route
                path="self-evolution/:threadId"
                element={<SelfEvolutionDetailPage />}
              />
            </>
          )}
          <Route path="task-center" element={<TaskCenterPage />} />
          <Route
            path="settings/agent-integrations"
            element={isDesktopRuntime() ? <AgentIntegrationPage /> : <Navigate to="/agent/chat" replace />}
          />
        </Route>
        {runtimeFeatures.hideCloudAdmin ? (
          <Route
            path="/admin/*"
            element={<Navigate to="/agent/chat" replace />}
          />
        ) : (
          <Route path="/admin" element={<AdminLayout />}>
            <Route index element={<Navigate to="groups" replace />} />
            <Route path="users" element={<UserManagement />} />
            <Route path="groups" element={<GroupManagement />} />
            <Route path="groups/:id" element={<GroupDetail />} />
          </Route>
        )}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      </Suspense>
    </ConfigProvider>
  );
}
