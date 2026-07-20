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
import KnowledgeApp from "@/modules/knowledge/KnowledgeApp";
import KnowledgeList from "@/modules/knowledge/pages/list";
import KnowledgeAuth from "@/modules/knowledge/pages/auth";
import KnowledgeDetail from "@/modules/knowledge/pages/detail";
import Knowledge from "@/modules/knowledge/pages/knowledge";
import AdminLayout from "@/modules/admin/AdminLayout";
import TaskCenterPage from "@/modules/taskCenter";
import UserManagement from "@/modules/admin/pages/user";
import GroupManagement from "@/modules/admin/pages/group";
import GroupDetail from "@/modules/admin/pages/group/detail.tsx";
import DatabaseConnectionsPage from "@/modules/dataSource/database";
import DataSourceFeishuCallback from "@/modules/dataSource/common/feishuCallback";
import CloudDocumentsPage from "@/modules/modelProvider/pages/CloudDocumentsPage";
import FeishuAccountPage from "@/modules/modelProvider/pages/FeishuAccountPage";
import GoogleDriveConnectionPage from "@/modules/modelProvider/pages/GoogleDriveConnectionPage";
import GoogleDriveSetupGuide from "@/modules/modelProvider/pages/GoogleDriveSetupGuide";
import LocalDataSourcePage from "@/modules/modelProvider/pages/LocalDataSourcePage";
import FeishuSetupGuide from "@/modules/modelProvider/pages/FeishuSetupGuide";
import NotionSetupGuide from "@/modules/modelProvider/pages/NotionSetupGuide";
import DatasetListPage from "@/modules/datasetManagement/pages/list";
import DatasetDetailPage from "@/modules/datasetManagement/pages/detail";
import MemoryManagement from "@/modules/memory";
import MemoryManagementListPage from "@/modules/memory/pages/list";
import MemoryReviewPage from "@/modules/memory/pages/review";
import MemoryGlossaryDetailPage from "@/modules/memory/pages/glossaryDetail";
import MemorySkillDetailPage from "@/modules/memory/pages/skillDetail";
import MemoryExperienceDetailPage from "@/modules/memory/pages/experienceDetail";
import ModelProviderPage from "@/modules/modelProvider";
import CloudDocumentsLayout from "@/modules/modelProvider/CloudDocumentsLayout";
import ModelProvidersPage from "@/modules/modelProvider/pages/ModelProvidersPage";
import ExternalServicesPage from "@/modules/modelProvider/pages/ExternalServicesPage";
import DefaultServicesPage from "@/modules/modelProvider/pages/DefaultServicesPage";
import {
  SelfEvolutionAlgorithmManagementPage,
  SelfEvolutionRoutingStrategyPage,
  SelfEvolutionHomePage,
  SelfEvolutionDetailPage,
  SelfEvolutionObservationPage,
} from "@/modules/selfEvolution";
import { getAntdLocale } from "@/i18n/antdLocale";
import { runtimeFeatures } from "@/runtime/features";
import { isLocalSessionEnabled } from "@/runtime/localSession";

const PluginDetailPage = lazy(() => import("@/modules/plugin/pages/detail"));
const BuiltinPluginDetailPage = lazy(() => import("@/modules/plugin/pages/builtin-detail"));

export default function AppRouter() {
  const { i18n } = useTranslation();
  const localSessionEnabled = isLocalSessionEnabled();

  return (
    <ConfigProvider
      locale={getAntdLocale(i18n.resolvedLanguage || i18n.language)}
    >
      <Routes>
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
              element={<MemoryExperienceDetailPage />}
            />
            <Route path="glossary" element={<MemoryManagementListPage />} />
            <Route
              path="glossary/:itemId"
              element={<MemoryGlossaryDetailPage />}
            />
            <Route path="review/:tab/:itemId" element={<MemoryReviewPage />} />
          </Route>
          <Route path="memory-management/plugins" element={<Navigate to="/memory-management/skills?skillView=plugins" replace />} />
          <Route path="memory-management/plugins/builtin/:pluginId" element={<Suspense fallback={<Spin style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }} />}><BuiltinPluginDetailPage /></Suspense>} />
          <Route path="memory-management/plugins/:pluginId" element={<Suspense fallback={<Spin style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }} />}><PluginDetailPage /></Suspense>} />
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
    </ConfigProvider>
  );
}
