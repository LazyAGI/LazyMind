import { Routes, Route, Navigate } from "react-router-dom";
import { ConfigProvider } from "antd";
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
import UserManagement from "@/modules/admin/pages/user";
import GroupManagement from "@/modules/admin/pages/group";
import GroupDetail from "@/modules/admin/pages/group/detail.tsx";
import DataSourceManagement from "@/modules/dataSource";
import DataSourceDetail from "@/modules/dataSource/detail";
import DataSourceFeishuCallback from "@/modules/dataSource/common/feishuCallback";
import FeishuAccountPage from "@/modules/dataSource/feishuAccounts";
import FeishuSetupGuide from "@/modules/dataSource/FeishuSetupGuide";
import NotionSetupGuide from "@/modules/dataSource/NotionSetupGuide";
import DatasetListPage from "@/modules/datasetManagement/pages/list";
import DatasetDetailPage from "@/modules/datasetManagement/pages/detail";
import MemoryManagement from "@/modules/memory";
import MemoryManagementListPage from "@/modules/memory/pages/list";
import MemoryReviewPage from "@/modules/memory/pages/review";
import MemoryGlossaryDetailPage from "@/modules/memory/pages/glossaryDetail";
import MemorySkillDetailPage from "@/modules/memory/pages/skillDetail";
import MemoryExperienceDetailPage from "@/modules/memory/pages/experienceDetail";
import ModelProviderPage from "@/modules/modelProvider";
import ModelProvidersPage from "@/modules/modelProvider/pages/ModelProvidersPage";
import ExternalServicesPage from "@/modules/modelProvider/pages/ExternalServicesPage";
import DefaultServicesPage from "@/modules/modelProvider/pages/DefaultServicesPage";
import { SelfEvolutionHomePage, SelfEvolutionDetailPage, SelfEvolutionObservationPage } from "@/modules/selfEvolution";
import { getAntdLocale } from "@/i18n/antdLocale";
import { runtimeFeatures } from "@/runtime/features";

export default function AppRouter() {
  const { i18n } = useTranslation();

  return (
    <ConfigProvider locale={getAntdLocale(i18n.resolvedLanguage || i18n.language)}>
      <Routes>
        <Route path="/login" element={<SigninDashboard />}>
          <Route index element={<SigninLogin />} />
        </Route>
        {runtimeFeatures.hideRegister ? (
          <Route path="/register" element={<Navigate to="/login" replace />} />
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
        <Route path="/loginTransition" element={<LoginTransition />} />
        <Route path="/" element={<MainLayout />}>
          <Route index element={<Navigate to="/agent/chat" replace />} />
          <Route path="agent/chat" element={<ChatApp />}>
            <Route index element={<Navigate to="home" replace />} />
            <Route path="home" element={<Home />} />
          </Route>
          <Route path="lib/knowledge" element={<KnowledgeApp />}>
            <Route index element={<Navigate to="list" replace />} />
            <Route path="list" element={<KnowledgeList />} />
            <Route path="auth/:id" element={<KnowledgeAuth />} />
            <Route path="detail/:id" element={<KnowledgeDetail />} />
            <Route
              path="knowledge/:knowledgeBaseId/:knowledgeId"
              element={<Knowledge />}
            />
          </Route>
          <Route path="data-sources" element={<DataSourceManagement />} />
          <Route path="data-sources/docs/feishu-setup" element={<FeishuSetupGuide />} />
          <Route path="data-sources/docs/notion-setup" element={<NotionSetupGuide />} />
          <Route path="data-sources/providers/feishu" element={<FeishuAccountPage />} />
          <Route path="data-sources/providers/notion" element={<DataSourceManagement />} />
          <Route path="data-sources/providers/sciverse" element={<Navigate to="/model-providers/tools" replace />} />
          <Route path="data-sources/:id" element={<DataSourceDetail />} />
          <Route path="dataset-management" element={<DatasetListPage />} />
          <Route path="dataset-management/:datasetId" element={<DatasetDetailPage />} />
          <Route path="model-providers" element={<ModelProviderPage />}>
            <Route index element={<Navigate to="default-services" replace />} />
            <Route path="models" element={<ModelProvidersPage />} />
            <Route path="document-parsing" element={<ExternalServicesPage section="parsing" />} />
            <Route path="tools" element={<ExternalServicesPage section="tools" />} />
            <Route path="external-services" element={<Navigate to="/model-providers/document-parsing" replace />} />
            <Route path="default-services" element={<DefaultServicesPage />} />
          </Route>
          <Route path="memory-management" element={<MemoryManagement />}>
            <Route index element={<MemoryManagementListPage />} />
            <Route path="tools" element={<Navigate to="/model-providers/tools" replace />} />
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
            <Route
              path="review/:tab/:itemId"
              element={<MemoryReviewPage />}
            />
          </Route>
          {runtimeFeatures.hideEvo ? (
            <Route path="self-evolution/*" element={<Navigate to="/agent/chat" replace />} />
          ) : (
            <>
              <Route path="self-evolution" element={<SelfEvolutionHomePage />} />
              <Route path="self-evolution/detail/:threadId/observation/:kind" element={<SelfEvolutionObservationPage />} />
              <Route path="self-evolution/detail/:threadId" element={<SelfEvolutionDetailPage />} />
              <Route path="self-evolution/:threadId/observation/:kind" element={<SelfEvolutionObservationPage />} />
              <Route path="self-evolution/:threadId" element={<SelfEvolutionDetailPage />} />
            </>
          )}
        </Route>
        {runtimeFeatures.hideCloudAdmin ? (
          <Route path="/admin/*" element={<Navigate to="/agent/chat" replace />} />
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
