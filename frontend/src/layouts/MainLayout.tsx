import { useConversationUnreadStore } from "@/modules/chat/store/conversationUnread";
import { Suspense, useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Badge, Button, Form, Input, Layout, Modal, Popover, Spin, message } from "antd";
import type { InputRef } from "antd";
import {
  CodeOutlined,
  SettingOutlined,
  SearchOutlined,
  AppstoreOutlined,
  DatabaseOutlined,
  TableOutlined,
  ApiOutlined,
  UserOutlined,
  GlobalOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  PlusOutlined,
  RightOutlined,
  FolderOpenOutlined,
  UnorderedListOutlined,
  HistoryOutlined,
  BookOutlined,
  CloudOutlined,
  LinkOutlined,
  LoginOutlined,
  LogoutOutlined,
} from "@ant-design/icons";
import { matchPath, Navigate, Outlet, useLocation, useNavigate } from "react-router-dom";
import type { UserDetailResponse } from "@/api/generated/auth-client";
import type { Conversation } from "@/api/generated/chatbot-client";
import { AUTH_USER_CHANGE_EVENT, AgentAppsAuth } from "@/components/auth";
import {
  changeCurrentUserPassword,
  fetchCurrentUser,
  fetchCurrentUserDetail,
  updateCurrentUserProfile,
} from "@/modules/signin/utils/request";
import { validatePassword } from "@/modules/signin/utils/formRules";
import logoImage from "@/public/Lazy.png";
import { useTranslation } from "react-i18next";
import LanguageSwitcher from "@/components/LanguageSwitcher";
import { isVocabularyEnabled } from "@/runtime/mode";
import {
	DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
  syncDeveloperModeFromServer,
} from "@/utils/developerMode";
import { syncSensitiveWordFilterFromServer } from "@/utils/sensitiveWordFilter";
import RecordList, {
  type RecordListImperativeProps,
} from "@/modules/chat/components/RecordList";
import { useConversationRunningSync } from "@/modules/chat/store/conversationRunning";
import {
  CHAT_CONVERSATION_FILTER_EVENT,
  CHAT_CONVERSATION_LIST_REFRESH_EVENT,
  type ChatConversationFilter,
  CHAT_HOME_PATH,
  CHAT_PENDING_CONVERSATION_GROUP_KEY,
  CHAT_SELECT_CONVERSATION_EVENT,
  getChatConversationPath,
  selectChatConversationFilter,
  readChatConversationFilters,
} from "@/modules/chat/constants/chat";
import { runtimeFeatures } from "@/runtime/features";
import { shouldHideLocalUserControls } from "@/runtime/localSession";
import { useLocalSessionGate } from "@/runtime/useLocalSessionGate";
import UserAgreementConsentModal, {
  useUserAgreementConsentGate,
} from "@/components/UserAgreementConsentModal";
import TerminalConnectionQuickPanel from "@/modules/channelGateway/components/TerminalConnectionQuickPanel";
import { beginCloudLogin, getCloudSession, LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, logoutCloudSession, type CloudSessionState } from "@/runtime/cloud/session";
import {
  closeCloudLoginPopup,
  openCloudLogin,
  openCloudRegister,
  reserveCloudLoginPopup,
} from "@/runtime/desktopBridge";
import { useConversationOpening } from "@/modules/chat/hooks/useConversationOpening";
import ConversationGroups from "@/modules/chat/conversationOrganizer/ConversationGroups";
import SidebarResizeHandle, { normalizeSidebarWidth, SIDEBAR_MAX_WIDTH, SIDEBAR_MIN_WIDTH } from "./SidebarResizeHandle";
import "./index.scss";

const { Content, Sider } = Layout;
const MAINLAND_CHINA_PHONE_REGEX = /^1[3-9]\d{9}$/;
const PROFILE_NICKNAME_MAX_LENGTH = 50;
const PROFILE_EMAIL_MAX_LENGTH = 30;
const PROFILE_PHONE_MAX_LENGTH = 11;
const PROFILE_DESCRIPTION_MAX_LENGTH = 200;
const PROFILE_PASSWORD_MAX_LENGTH = 32;

function isAdminRole(role?: string) {
  const normalizedRole = (role || "").trim().toLowerCase();
  return (
    normalizedRole === "admin" ||
    normalizedRole === "system-admin" ||
    normalizedRole === "system_admin" ||
    normalizedRole.endsWith(".admin")
  );
}

function readChatConversationMode(): ChatConversationFilter {
  return readChatConversationFilters().filter;
}
interface ProfileFormValues {
  username: string;
  displayName?: string;
  email?: string;
  phone?: string;
  remark?: string;
  roleName?: string;
  status?: string;
  currentPassword?: string;
  newPassword?: string;
  confirmPassword?: string;
}

function normalizeFieldValue(value?: string | null) {
  return (value || "").trim();
}

export default function MainLayout() {
  const location = useLocation();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [profileForm] = Form.useForm<ProfileFormValues>();
  const pathname = location.pathname || "/agent/chat";
  // The detail URL is the source of truth for sidebar selection across reloads.
  const routeConversationId =
    matchPath(`${CHAT_HOME_PATH}/:conversationId`, pathname)?.params
      .conversationId || "";

  const unreadCount = useConversationUnreadStore(state => state.counts[routeConversationId] || 0);

  const [userInfo, setUserInfo] = useState(() => AgentAppsAuth.getUserInfo());
  const isLoggedIn = Boolean(userInfo?.token);
  useConversationRunningSync(isLoggedIn ? userInfo?.userId || userInfo?.username || "" : "", routeConversationId);
  const userName = userInfo?.username || "";
  const isAdminUser = isAdminRole(userInfo?.role);
  const hideLocalUserControls = shouldHideLocalUserControls();
  const accountDisplayName = userInfo?.displayName || userName || "LazyMind";
  const accountRoleLabel = isAdminUser
    ? t("layout.systemAdministrator")
    : t("layout.normalUser");

  const [currentSidebarConversationId, setCurrentSidebarConversationId] =
    useState(routeConversationId);
  const currentSidebarConversationIdRef = useRef(
    currentSidebarConversationId,
  );
  const recordListRef = useRef<RecordListImperativeProps>(null);
  const refreshOpeningTitles = useCallback(() => { window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT)); }, []);
  useConversationOpening(isLoggedIn ? userName : "", refreshOpeningTitles);
  currentSidebarConversationIdRef.current = currentSidebarConversationId;
  const [profileModalOpen, setProfileModalOpen] = useState(false);
  const [profileLoading, setProfileLoading] = useState(false);
  const [profileSubmitting, setProfileSubmitting] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [terminalConnectionOpen, setTerminalConnectionOpen] = useState(false);
  const [sidebarSearchText, setSidebarSearchText] = useState("");
  const [chatConversationMode, setChatConversationMode] =
    useState<ChatConversationFilter>(readChatConversationMode);
  const isWorkflowDetail = Boolean(
    matchPath("/memory-management/workflows/:workflowId", pathname) ||
    matchPath("/memory-management/workflows/builtin/:workflowId", pathname) ||
    matchPath("/memory-management/workflows/published/:workflowRef", pathname) ||
    matchPath("/memory-management/workflows/cloud/:resourceId", pathname),
  );
  const defaultSidebarWidth = isWorkflowDetail ? SIDEBAR_MIN_WIDTH : SIDEBAR_MAX_WIDTH;
  const [sidebarSize, setSidebarSize] = useState({
    pathname,
    width: defaultSidebarWidth,
    normalWidth: SIDEBAR_MAX_WIDTH,
  });
  // Workflow details start narrow without overwriting the width chosen for other pages.
  const routeSidebarWidth = isWorkflowDetail ? SIDEBAR_MIN_WIDTH : sidebarSize.normalWidth;
  if (sidebarSize.pathname !== pathname) {
    setSidebarSize({ ...sidebarSize, pathname, width: routeSidebarWidth });
  }
  const sidebarWidth = normalizeSidebarWidth(
    sidebarSize.pathname === pathname ? sidebarSize.width : routeSidebarWidth,
  );
  const isMenuCollapsed = sidebarWidth === SIDEBAR_MIN_WIDTH;
  const resizeSidebar = (width: number) => setSidebarSize({
    pathname,
    width,
    normalWidth: isWorkflowDetail ? sidebarSize.normalWidth : width,
  });
  const sidebarSearchRef = useRef<InputRef>(null);
  const sidebarHistoryRef = useRef<HTMLDivElement>(null);
  const [sidebarFocusTarget, setSidebarFocusTarget] = useState<"search" | "history" | null>(null);
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const [cloudSessionState, setCloudSessionState] = useState<CloudSessionState>("signed_out");
  const updateCloudSessionState = useCallback((state: CloudSessionState) => {
    setCloudSessionState(state);
    window.dispatchEvent(new CustomEvent(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, {detail: {state}}));
  }, []);
  const [cloudAccountName, setCloudAccountName] = useState("");
  const [cloudRegistrationURL, setCloudRegistrationURL] = useState("");
  const [cloudLoginLoading, setCloudLoginLoading] = useState(false);
  const [profileDetail, setProfileDetail] = useState<UserDetailResponse | null>(null);

  const settingsMenuItems = [
    {
      key: "/settings?section=overview",
      label: t("layout.settings"),
      icon: (
        <SettingOutlined className="settings-popover-icon" aria-hidden="true" />
      ),
    },
    {
      key: "/settings?section=models",
      label: t("layout.modelProviderManagement"),
      icon: (
        <ApiOutlined className="settings-popover-icon" aria-hidden="true" />
      ),
    },
    {
      key: "/settings?section=developer",
      label: t("layout.developer"),
      icon: (
        <CodeOutlined
          className="settings-popover-icon"
          aria-hidden="true"
        />
      ),
    },
  ];
  const showSettingsTrigger =
    settingsMenuItems.length > 0 || !hideLocalUserControls;
  const resourceNavItems = [
    {
      key: "/lib/knowledge",
      label: t("layout.knowledgeBase"),
      icon: <AppstoreOutlined />,
    },
    {
      key: "/cloud-documents",
      label: t("layout.cloudDocuments"),
      icon: <CloudOutlined />,
    },
    ...(isVocabularyEnabled() ? [{
      key: "/lib/vocabulary",
      label: "生词表",
      icon: <BookOutlined />,
    }] : []),
    ...(developerActive ? [{
      key: "/dataset-management",
      label: t("layout.datasetManagement"),
      icon: <TableOutlined />,
    }, {
      key: "/databases",
      label: t("layout.database"),
      icon: <DatabaseOutlined />,
    }] : []),
  ];
  const hideEvo = runtimeFeatures.hideEvo;
  const canAccessSelfEvolution = !hideEvo && developerActive && isAdminUser;
  const logoSrc =
    (import.meta.env as ImportMetaEnv & { VITE_APP_LOGO?: string })
      .VITE_APP_LOGO || "";
  const isChatPage = pathname.startsWith("/agent/chat");
  const contentClassName = [
    "main-layout-content",
    isChatPage ? "is-chat-page" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const refreshLayoutUser = useCallback(async () => {
    if (!AgentAppsAuth.isLoggedIn()) {
      setUserInfo(AgentAppsAuth.getUserInfo());
      return;
    }

    try {
      await fetchCurrentUser();
      const [devActive] = await Promise.all([
        syncDeveloperModeFromServer(),
        syncSensitiveWordFilterFromServer(),
      ]);
      setDeveloperActive(devActive);
    } catch (error) {
      console.error("Failed to refresh current user:", error);
    } finally {
      setUserInfo(AgentAppsAuth.getUserInfo());
    }
  }, []);
  const refreshCloudSession = useCallback(async () => {
    try {
      const session = await getCloudSession();
      updateCloudSessionState(session.state);
      setCloudAccountName(session.username || session.email_masked || "");
      setCloudRegistrationURL(session.registration_url || "");
    } catch {
      updateCloudSessionState("offline");
      setCloudAccountName("");
      setCloudRegistrationURL("");
    }
  }, [updateCloudSessionState]);
  const localSessionGate = useLocalSessionGate(refreshLayoutUser);
  const {
    needsConsent,
    markAccepted,
    loading: agreementLoading,
    checkFailed: agreementCheckFailed,
    retryCheck: retryAgreementCheck,
  } = useUserAgreementConsentGate(isLoggedIn);

  useEffect(() => {
    if (!localSessionGate.enabled) {
      refreshLayoutUser();
    }
    void refreshCloudSession();

    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        refreshLayoutUser();
        void refreshCloudSession();
      }
    };
    const handleFocus = () => {
      refreshLayoutUser();
      void refreshCloudSession();
    };
    const handleStorage = (event: StorageEvent) => {
      if (event.key === "lazymind:user") {
        setUserInfo(AgentAppsAuth.getUserInfo());
      }
    };
    const handleUserChange = () => {
      setUserInfo(AgentAppsAuth.getUserInfo());
    };
    const handleDeveloperModeChange = (event: Event) => {
      setDeveloperActive(
        Boolean((event as CustomEvent<{ active?: boolean }>).detail?.active),
      );
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    window.addEventListener("focus", handleFocus);
    window.addEventListener("storage", handleStorage);
    window.addEventListener(AUTH_USER_CHANGE_EVENT, handleUserChange);
    window.addEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperModeChange);

    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.removeEventListener("focus", handleFocus);
      window.removeEventListener("storage", handleStorage);
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, handleUserChange);
      window.removeEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperModeChange);
    };
  }, [localSessionGate.enabled, refreshCloudSession, refreshLayoutUser]);

  useEffect(() => {
    if (pathname.startsWith("/self-evolution") && !canAccessSelfEvolution) {
      navigate("/agent/chat", { replace: true });
    }
  }, [pathname, navigate, canAccessSelfEvolution]);

  useEffect(() => {
    if (!pathname.startsWith("/agent/chat")) {
      setCurrentSidebarConversationId("");
    }
  }, [pathname]);

  useEffect(() => {
    if (isMenuCollapsed || !sidebarFocusTarget) return;
    if (sidebarFocusTarget === "search") sidebarSearchRef.current?.focus();
    else sidebarHistoryRef.current?.focus();
    setSidebarFocusTarget(null);
  }, [isMenuCollapsed, sidebarFocusTarget]);

  useEffect(() => {
    const handleFilterChange = (event: Event) => {
      const filter = (
        event as CustomEvent<{ filter?: ChatConversationFilter }>
      ).detail?.filter;
      if (filter !== "normal" && filter !== "task") {
        return;
      }
      setChatConversationMode(filter);
    };

    window.addEventListener(
      CHAT_CONVERSATION_FILTER_EVENT,
      handleFilterChange,
    );
    return () => {
      window.removeEventListener(
        CHAT_CONVERSATION_FILTER_EVENT,
        handleFilterChange,
      );
    };
  }, []);

  useEffect(() => {
    const handleConversationListRefresh = () => {
      recordListRef.current?.refresh();
    };

    window.addEventListener(
      CHAT_CONVERSATION_LIST_REFRESH_EVENT,
      handleConversationListRefresh,
    );
    return () => {
      window.removeEventListener(
        CHAT_CONVERSATION_LIST_REFRESH_EVENT,
        handleConversationListRefresh,
      );
    };
  }, []);

  useEffect(() => {
    const handleConversationSelect = (event: Event) => {
      const detail = (
        event as CustomEvent<{ conversationId?: string; source?: string }>
      ).detail;
      const conversationId = detail?.conversationId || "";
      setCurrentSidebarConversationId(conversationId);

      if (
        !pathname.startsWith(CHAT_HOME_PATH) ||
        (detail?.source !== "chat" && detail?.source !== "mention")
      ) {
        return;
      }
      const targetPath = conversationId
        ? getChatConversationPath(conversationId)
        : CHAT_HOME_PATH;
      if (pathname !== targetPath) {
        navigate(targetPath, { replace: detail.source === "chat" });
      }
    };

    window.addEventListener(
      CHAT_SELECT_CONVERSATION_EVENT,
      handleConversationSelect,
    );
    return () => {
      window.removeEventListener(
        CHAT_SELECT_CONVERSATION_EVENT,
        handleConversationSelect,
      );
    };
  }, [navigate, pathname]);

  useEffect(() => {
    currentSidebarConversationIdRef.current = routeConversationId;
    setCurrentSidebarConversationId(routeConversationId);
  }, [routeConversationId]);

  const toggleMenu = () => {
    resizeSidebar(isMenuCollapsed ? SIDEBAR_MAX_WIDTH : SIDEBAR_MIN_WIDTH);
  };

  const openSidebarSection = (target: "search" | "history") => {
    resizeSidebar(SIDEBAR_MAX_WIDTH);
    setSidebarFocusTarget(target);
  };

  const emitConversationSelection = (
    conversationId: string,
    runInBackground = false,
  ) => {
    window.dispatchEvent(
      new CustomEvent(CHAT_SELECT_CONVERSATION_EVENT, {
        detail: { conversationId, runInBackground, source: "sidebar" },
      }),
    );
  };

  const handleNewChat = (runInBackground = false) => {
    sessionStorage.removeItem(CHAT_PENDING_CONVERSATION_GROUP_KEY);
    selectChatConversationFilter(runInBackground ? "task" : "normal");
    setCurrentSidebarConversationId("");
    emitConversationSelection("", runInBackground);
    navigate(CHAT_HOME_PATH);
  };

  const handleNewChatInGroup = (groupId: string) => {
    handleNewChat(false);
    sessionStorage.setItem(CHAT_PENDING_CONVERSATION_GROUP_KEY, groupId);
  };

  const handleSidebarConversationSelected = (conversation: Conversation) => {
    const conversationId = conversation.conversation_id || "";
    if (!conversationId) {
      return;
    }
    setCurrentSidebarConversationId(conversationId);
    navigate(getChatConversationPath(conversationId));
  };

  const handleSidebarConversationRemoved = (conversation: Conversation) => {
    const conversationId = conversation.conversation_id || "";
    if (
      !conversationId ||
      conversationId !== currentSidebarConversationIdRef.current
    ) {
      return;
    }
    currentSidebarConversationIdRef.current = "";
    setCurrentSidebarConversationId("");
    emitConversationSelection("");
    navigate(CHAT_HOME_PATH, { replace: true });
  };

  const handleModuleNavigate = (targetPath: string) => {
    setCurrentSidebarConversationId("");
    navigate(targetPath);
  };

  const isTaskMode = chatConversationMode === "task";

  const renderModulePopover = (
    items: Array<{ key: string; label: string; icon: ReactNode }>,
  ) => (
    <div className="sider-module-popover">
      {items.map((item) => (
        <Button
          key={item.key}
          type="text"
          className="sider-module-popover-item"
          icon={item.icon}
          onClick={() => handleModuleNavigate(item.key)}
        >
          {item.label}
        </Button>
      ))}
    </div>
  );

  const renderAiEvolutionPopover = () => (
    <div className="sider-module-popover sider-module-popover--grouped">
      <div className="sider-module-popover-group">
        <div className="sider-module-popover-group-header">
          <AppstoreOutlined />
          <span>{t("layout.memoryManagement")}</span>
        </div>
        {[
          { key: "/memory-management/skills", label: t("admin.memoryTabSkills"), icon: <AppstoreOutlined /> },
          { key: "/memory-management/experience", label: t("admin.memoryTabExperience"), icon: <HistoryOutlined /> },
          { key: "/memory-management/glossary", label: t("admin.memoryTabGlossary"), icon: <BookOutlined /> },
        ].map((item) => (
          <Button
            key={item.key}
            type="text"
            className="sider-module-popover-item sider-module-popover-item--sub"
            onClick={() => handleModuleNavigate(item.key)}
          >
            {item.icon}
            {item.label}
          </Button>
        ))}
      </div>
      {canAccessSelfEvolution && (
        <div className="sider-module-popover-group">
          <Button
            type="text"
            className="sider-module-popover-item"
            icon={<CodeOutlined />}
            onClick={() => handleModuleNavigate("/self-evolution")}
          >
            {t("layout.selfEvolution")}
          </Button>
        </div>
      )}
    </div>
  );

  const handleSettingsNavigate = (targetPath: string) => {
    setSettingsOpen(false);
    navigate(targetPath);
  };

  const handleLogout = () => {
    AgentAppsAuth.logout(
      `${window.location.origin}${window.BASENAME || ""}/login`,
    );
  };

  const handleGoLogin = () => {
    setSettingsOpen(false);
    navigate("/login");
  };

  const handleCloudLogout = async () => {
    try {
      updateCloudSessionState((await logoutCloudSession()).state);
      setCloudAccountName("");
      message.success(t("layout.cloudLogoutSuccess"));
    } catch {
      updateCloudSessionState("signed_out");
      setCloudAccountName("");
    }
  };

  const handleCloudLogin = async () => {
    const popup = reserveCloudLoginPopup();
    if (popup === null) {
      message.error(t("layout.cloudOpenFailed"));
      return;
    }
    setCloudLoginLoading(true);
    try {
      const login = await beginCloudLogin();
      const result = await openCloudLogin(login.authorization_url, popup);
      if (!result.ok) {
        await logoutCloudSession().catch(() => undefined);
        throw result.error || new Error(result.reason);
      }
      updateCloudSessionState("authorizing");
      setSettingsOpen(false);
    } catch {
      closeCloudLoginPopup(popup);
      message.error(t("layout.cloudLoginFailed"));
      await refreshCloudSession();
    } finally {
      setCloudLoginLoading(false);
    }
  };

  const handleCloudRegister = async () => {
    let registrationURL = cloudRegistrationURL;
    if (!registrationURL) {
      try {
        registrationURL = (await getCloudSession()).registration_url || "";
      } catch {
        registrationURL = "";
      }
    }
    const result = await openCloudRegister(registrationURL);
    if (!result.ok) {
      message.error(t("layout.cloudOpenFailed"));
    }
  };

  const currentPasswordRule = ({ getFieldValue }: any) => ({
    validator(_: any, value: string) {
      const newPassword = getFieldValue("newPassword");
      const confirmPassword = getFieldValue("confirmPassword");
      if (!newPassword && !confirmPassword && !value) {
        return Promise.resolve();
      }
      if (!value) {
        return Promise.reject(new Error(t("profile.pleaseInputCurrentPasswordRequired")));
      }
      return Promise.resolve();
    },
  });

  const passwordRequiredRule = ({ getFieldValue }: any) => ({
    validator(_: any, value: string) {
      const currentPassword = getFieldValue("currentPassword");
      const confirmPassword = getFieldValue("confirmPassword");
      if (!currentPassword && !confirmPassword && !value) {
        return Promise.resolve();
      }
      if (!value) {
        return Promise.reject(new Error(t("profile.pleaseInputNewPasswordRequired")));
      }
      return validatePassword(value);
    },
  });

  const confirmPasswordRule = ({ getFieldValue }: any) => ({
    validator(_: any, value: string) {
      const currentPassword = getFieldValue("currentPassword");
      const newPassword = getFieldValue("newPassword");
      if (!currentPassword && !newPassword && !value) {
        return Promise.resolve();
      }
      if (!value) {
        return Promise.reject(new Error(t("profile.pleaseConfirmNewPassword")));
      }
      if (value !== newPassword) {
        return Promise.reject(new Error(t("profile.passwordNotMatch")));
      }
      return Promise.resolve();
    },
  });

  const phoneRule = {
    validator(_: any, value?: string) {
      const phone = normalizeFieldValue(value);
      if (!phone || MAINLAND_CHINA_PHONE_REGEX.test(phone)) {
        return Promise.resolve();
      }
      return Promise.reject(new Error(t("profile.invalidPhone")));
    },
  };

  const clearPasswordFields = () => {
    profileForm.setFieldsValue({
      currentPassword: "",
      newPassword: "",
      confirmPassword: "",
    });
  };

  const schedulePasswordFieldClear = () => {
    window.setTimeout(() => {
      clearPasswordFields();
    }, 0);
    window.setTimeout(() => {
      clearPasswordFields();
    }, 300);
  };

  const applyProfileToForm = (detail: UserDetailResponse) => {
    profileForm.setFieldsValue({
      username: detail.username,
      displayName: detail.display_name || "",
      email: detail.email || "",
      phone: detail.phone || "",
      remark: (detail as any).remark || "",
      roleName: detail.role_name || "",
      status: detail.status || "",
    });
    clearPasswordFields();
  };

  const refreshCurrentProfile = async () => {
    const detail = await fetchCurrentUserDetail();
    setUserInfo(AgentAppsAuth.getUserInfo());
    setProfileDetail(detail);
    applyProfileToForm(detail);
    return detail;
  };

  const handleOpenProfile = async () => {
    setProfileModalOpen(true);
    setProfileLoading(true);
    try {
      await refreshCurrentProfile();
    } catch {
      setProfileModalOpen(false);
    } finally {
      setProfileLoading(false);
    }
  };

  const handleCloseProfile = () => {
    setProfileModalOpen(false);
    setProfileLoading(false);
    setProfileSubmitting(false);
    setProfileDetail(null);
    profileForm.resetFields();
  };

  const handleProfileSubmit = async () => {
    try {
      const values = await profileForm.validateFields();
      if (!profileDetail?.user_id) {
        message.error(t("profile.noUserInfo"));
        return;
      }

      const payload: {
        display_name?: string;
        email?: string;
        phone?: string;
        remark?: string;
      } = {};
      const nextDisplayName = normalizeFieldValue(values.displayName);
      const nextEmail = normalizeFieldValue(values.email);
      const nextPhone = normalizeFieldValue(values.phone);
      const nextRemark = normalizeFieldValue(values.remark);
      const currentPassword = values.currentPassword || "";
      const newPassword = values.newPassword || "";

      if (
        nextDisplayName !== normalizeFieldValue(profileDetail.display_name || "")
      ) {
        payload.display_name = nextDisplayName;
      }
      if (nextEmail !== normalizeFieldValue(profileDetail.email || "")) {
        payload.email = nextEmail;
      }
      if (nextPhone !== normalizeFieldValue(profileDetail.phone || "")) {
        payload.phone = nextPhone;
      }
      if (nextRemark !== normalizeFieldValue((profileDetail as any).remark || "")) {
        payload.remark = nextRemark;
      }

      const shouldUpdateProfile = Object.keys(payload).length > 0;
      const shouldUpdatePassword = Boolean(currentPassword || newPassword);

      if (!shouldUpdateProfile && !shouldUpdatePassword) {
      message.info(t("profile.noChanges"));
        return;
      }

      setProfileSubmitting(true);

      if (shouldUpdateProfile) {
        await updateCurrentUserProfile(payload);
      }

      if (shouldUpdatePassword) {
        await changeCurrentUserPassword(currentPassword, newPassword);
      }

      await refreshCurrentProfile();
      message.success(t("profile.updateSuccess"));
      handleCloseProfile();
    } catch (error: any) {
      if (!error?.errorFields) {
        console.error("Failed to update current user profile:", error);
      }
    } finally {
      setProfileSubmitting(false);
    }
  };

  if (localSessionGate.enabled && (localSessionGate.loading || localSessionGate.error)) {
    return (
      <div className="local-session-gate">
        <div className="local-session-panel">
          {localSessionGate.loading ? <Spin /> : null}
          <div className="local-session-title">LazyMind</div>
          <div className="local-session-message">
            {localSessionGate.error || t("layout.preparingLocalSession", "Preparing local session...")}
          </div>
          {localSessionGate.error ? (
            <Button
              type="primary"
              loading={localSessionGate.loading}
              onClick={localSessionGate.retry}
            >
              {t("common.retry", "Retry")}
            </Button>
          ) : null}
        </div>
      </div>
    );
  }

  if (!isLoggedIn) {
    return <Navigate to="/login" replace state={location.pathname === "/cloud-documents"
      ? { cloudDocumentReturnTo: `${location.pathname}${location.search}` }
      : undefined} />;
  }

  if (agreementLoading || agreementCheckFailed) {
    return (
      <div className="local-session-gate">
        <div className="local-session-panel">
          {agreementLoading ? <Spin /> : null}
          <div className="local-session-title">LazyMind</div>
          <div className="local-session-message">
            {agreementCheckFailed
              ? t("legal.consentCheckFailed")
              : t("legal.consentChecking")}
          </div>
          {agreementCheckFailed ? (
            <Button type="primary" onClick={retryAgreementCheck}>
              {t("common.retry", "Retry")}
            </Button>
          ) : null}
        </div>
      </div>
    );
  }

  return (
    <Layout hasSider className="main-layout">
      <Sider
        id="main-navigation"
        width={sidebarWidth}
        trigger={null}
        className={`sider-bar-style${isMenuCollapsed ? " is-collapsed" : ""}`}
      >
        <div className="sider-inner">
          <div className="sider-brand-row">
            <button
              type="button"
              className="sider-brand"
              onClick={() => handleNewChat(false)}
              aria-label="LazyMind"
              title="LazyMind"
            >
              <Badge count={unreadCount} size="small" overflowCount={99} title={t("chat.unreadAnswers", { count: unreadCount })}>
                <img src={logoSrc || logoImage} alt="logo" />
              </Badge>
              {unreadCount > 0 && <span className="sider-unread-status" role="status">{t("chat.unreadAnswers", { count: unreadCount })}</span>}
            </button>
            <button
              type="button"
              className="sider-inline-toggle"
              onClick={toggleMenu}
              aria-label={isMenuCollapsed ? t("layout.expandMenu") : t("layout.collapseMenu")}
              title={isMenuCollapsed ? t("layout.expandMenu") : t("layout.collapseMenu")}
            >
              {isMenuCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            </button>
          </div>
          <div className="sider-primary-action">
            <Button
              type="text"
              className={`sider-new-chat-button${!isTaskMode ? " is-active" : ""}`}
              icon={<PlusOutlined />}
              onClick={() => handleNewChat(false)}
              aria-pressed={!isTaskMode}
              aria-label={t("layout.newChat")}
              title={t("layout.newChat")}
            >
              {!isMenuCollapsed && t("layout.newChat")}
            </Button>
            <Button
              type="text"
              className={`sider-new-chat-button${isTaskMode ? " is-active" : ""}`}
              icon={<PlusOutlined />}
              onClick={() => handleNewChat(true)}
              aria-pressed={isTaskMode}
              aria-label={t("layout.newTask")}
              title={t("layout.newTask")}
            >
              {!isMenuCollapsed && t("layout.newTask")}
            </Button>
          </div>
          <div className="sider-module-actions">
            <Popover
              content={renderModulePopover(resourceNavItems)}
              arrow={false}
              placement="rightTop"
              trigger={["hover", "click"]}
              mouseLeaveDelay={0.25}
              align={{ offset: [-4, 0] }}
              overlayClassName="sider-module-overlay"
            >
              <button type="button" className="sider-module-trigger" aria-label={t("layout.resourceLib")} title={t("layout.resourceLib")}>
                <span className="sider-module-icon">
                  <FolderOpenOutlined />
                </span>
                <span className="sider-module-text">{t("layout.resourceLib")}</span>
                <RightOutlined className="sider-module-arrow" />
              </button>
            </Popover>
            <Popover
              content={renderAiEvolutionPopover()}
              arrow={false}
              placement="rightTop"
              trigger={["hover", "click"]}
              mouseLeaveDelay={0.25}
              align={{ offset: [-4, 0] }}
              overlayClassName="sider-module-overlay"
            >
              <button type="button" className="sider-module-trigger" aria-label={t("layout.aiEvolution")} title={t("layout.aiEvolution")}>
                <span className="sider-module-icon">
                  <CodeOutlined />
                </span>
                <span className="sider-module-text">{t("layout.aiEvolution")}</span>
                <RightOutlined className="sider-module-arrow" />
              </button>
            </Popover>
            <button
              type="button"
              className={`sider-module-trigger${pathname.startsWith("/task-center") ? " is-active" : ""}`}
              onClick={() => handleModuleNavigate("/task-center")}
              aria-label={t("layout.taskCenter")}
              title={t("layout.taskCenter")}
            >
              <span className="sider-module-icon">
                <UnorderedListOutlined />
              </span>
              <span className="sider-module-text">{t("layout.taskCenter")}</span>
            </button>
          </div>
          {isMenuCollapsed && (
            <div className="sider-compact-history">
              <button type="button" className="sider-module-trigger" aria-label={t("layout.searchConversations")} title={t("layout.searchConversations")} onClick={() => openSidebarSection("search")}>
                <SearchOutlined />
              </button>
              <button type="button" className="sider-module-trigger" aria-label={t("layout.conversationHistory")} title={t("layout.conversationHistory")} onClick={() => openSidebarSection("history")}>
                <HistoryOutlined />
              </button>
            </div>
          )}
          <div className="sider-history-search" hidden={isMenuCollapsed}>
            <Input
              ref={sidebarSearchRef}
              className="sider-history-search-input"
              type="search"
              prefix={<SearchOutlined />}
              allowClear
              value={sidebarSearchText}
              placeholder={t("conversationOrganizer.searchPlaceholder")}
              aria-label={t("conversationOrganizer.searchPlaceholder")}
              onChange={(event) => setSidebarSearchText(event.target.value)}
            />
          </div>
          <div className="sider-history" ref={sidebarHistoryRef} tabIndex={-1} aria-label={t("layout.conversationHistory")} hidden={isMenuCollapsed}>
            <RecordList
              ref={recordListRef}
              groupSection={(batchSelection) => <ConversationGroups
              batchSelection={batchSelection}
              mode="groups"
              searchText={sidebarSearchText}
              currentConversationId={currentSidebarConversationId}
              onChanged={() => recordListRef.current?.refresh()}
              onNewChatInGroup={handleNewChatInGroup}
            />}
              compact
              hideSearch
              showBatchActions
              title={t("chat.recentConversations")}
              searchText={sidebarSearchText}
              currentSessionId={currentSidebarConversationId}
              onSelected={handleSidebarConversationSelected}
              onRemove={handleSidebarConversationRemoved}
            />
          </div>
          <div className="sider-bar-bottom">
            {showSettingsTrigger && (
              <Popover
                content={
                  <div className="settings-popover" role="menu">
                    {userName && !hideLocalUserControls && (
                      <button
                        type="button"
                        className="settings-popover-account"
                        role="menuitem"
                        onClick={() => {
                          setSettingsOpen(false);
                          void handleOpenProfile();
                        }}
                      >
                        <span className="settings-popover-avatar" aria-hidden="true">
                          <UserOutlined />
                        </span>
                        <span className="settings-popover-account-copy">
                          <strong>{accountDisplayName}</strong>
                          <small>{accountRoleLabel}</small>
                        </span>
                      </button>
                    )}
                    <div className="settings-popover-cloud" data-cloud-session={cloudSessionState}>
                      <CloudOutlined className="settings-popover-icon" />
                      <span className="settings-popover-cloud-copy">
                        <strong>LazyMind Cloud</strong>
                        <small>
                          {cloudSessionState === "signed_in"
                            ? cloudAccountName || t("layout.cloudSignedIn")
                            : t("layout.cloudSignedOut")}
                        </small>
                      </span>
                      {cloudSessionState === "signed_in" ? (
                        <Button size="small" type="link" onClick={() => void handleCloudLogout()}>
                          {t("layout.cloudLogout")}
                        </Button>
                      ) : (
                        <span className="settings-popover-cloud-actions">
                          <Button size="small" type="link" loading={cloudLoginLoading} onClick={() => void handleCloudLogin()}>
                            {t("layout.cloudLogin")}
                          </Button>
                          <Button size="small" type="link" onClick={() => void handleCloudRegister()}>
                            {t("layout.cloudRegister")}
                          </Button>
                        </span>
                      )}
                    </div>
                    {settingsMenuItems.map((item) => {
                      const btn = (
                        <Button
                          key={item.key}
                          type="text"
                          role="menuitem"
                          className={`settings-popover-button${
                            item.key === "/settings?section=developer" && developerActive ? " is-active" : ""
                          }`}
                          onClick={() => handleSettingsNavigate(item.key)}
                        >
                          {item.icon}
                          <span className="settings-popover-label">{item.label}</span>
                          {item.key === "/settings?section=developer" && developerActive && (
                            <span className="settings-active-badge">{t("admin.developerActiveTag")}</span>
                          )}
                          {[
                            "/settings?section=overview",
                            "/settings?section=models",
                            "/settings?section=developer",
                          ].includes(item.key) && (
                            <RightOutlined
                              className="settings-popover-accessory"
                              aria-hidden="true"
                            />
                          )}
                        </Button>
                      );
                      return btn;
                    })}
                    <div className="settings-popover-language">
                      <GlobalOutlined
                        className="settings-popover-icon"
                        aria-hidden="true"
                      />
                      <LanguageSwitcher />
                    </div>
                    {!hideLocalUserControls && (
                      <div
                        className="settings-popover-separator"
                        role="separator"
                      />
                    )}
                    {!hideLocalUserControls && (
                      isLoggedIn ? (
                        <Button
                          type="text"
                          role="menuitem"
                          className="settings-popover-button settings-popover-button--session"
                          onClick={handleLogout}
                        >
                          <LogoutOutlined
                            className="settings-popover-icon"
                            aria-hidden="true"
                          />
                          <span className="settings-popover-label">
                            {t("layout.logout")}
                          </span>
                        </Button>
                      ) : (
                        <Button
                          type="text"
                          role="menuitem"
                          className="settings-popover-button settings-popover-button--session"
                          onClick={handleGoLogin}
                        >
                          <LoginOutlined
                            className="settings-popover-icon"
                            aria-hidden="true"
                          />
                          <span className="settings-popover-label">
                            {t("layout.goLogin")}
                          </span>
                        </Button>
                      )
                    )}
                  </div>
                }
                arrow={false}
                overlayClassName="settings-popover-overlay"
                placement="topLeft"
                trigger="click"
                open={settingsOpen}
                onOpenChange={(open) => {
                  setSettingsOpen(open);
                  if (open) setTerminalConnectionOpen(false);
                }}
              >
                <button
                  type="button"
                  className={`sider-account-trigger${
                    pathname.startsWith("/settings") ? " is-active" : ""
                  }`}
                  aria-label={t("layout.settings")}
                  aria-haspopup="menu"
                  aria-expanded={settingsOpen}
                  title={t("layout.settings")}
                >
                  <span className="sider-account-avatar" aria-hidden="true">
                    {isMenuCollapsed ? <SettingOutlined /> : <UserOutlined />}
                  </span>
                  {!isMenuCollapsed && (
                    <span className="sider-account-copy">
                      <strong>{accountDisplayName}</strong>
                      <small>{accountRoleLabel}</small>
                    </span>
                  )}
                </button>
              </Popover>
            )}
            <div className="sider-account-actions">
              <Popover
                content={(
                  <TerminalConnectionQuickPanel
                    onManage={() => {
                      setTerminalConnectionOpen(false);
                      handleModuleNavigate("/settings?section=channels");
                    }}
                  />
                )}
                arrow={false}
                overlayClassName="terminal-quick-popover-overlay"
                placement="topLeft"
                trigger="click"
                destroyOnHidden
                open={terminalConnectionOpen}
                onOpenChange={(open) => {
                  setTerminalConnectionOpen(open);
                  if (open) setSettingsOpen(false);
                }}
              >
                <button
                  type="button"
                  className={`sider-account-action${
                    terminalConnectionOpen || pathname.startsWith("/channels")
                      ? " is-active"
                      : ""
                  }`}
                  aria-label={t("layout.terminalConnection")}
                  title={t("layout.terminalConnection")}
                  aria-haspopup="dialog"
                  aria-expanded={terminalConnectionOpen}
                >
                  <LinkOutlined />
                </button>
              </Popover>
            </div>
          </div>
        </div>
        <SidebarResizeHandle key={pathname} width={sidebarWidth} onResize={resizeSidebar} />
      </Sider>
      <Layout className={contentClassName}>
        <Content className="main-layout-body">
          <div className="sub-app-container">
            <Suspense fallback={<div className="main-layout-page-loading"><Spin /></div>}>
              <Outlet
                context={{
                  isMenuCollapsed,
                  toggleMenu,
                }}
              />
            </Suspense>
          </div>
        </Content>
      </Layout>
      <Modal
        title={t("profile.title")}
        open={profileModalOpen}
        onCancel={handleCloseProfile}
        onOk={handleProfileSubmit}
        confirmLoading={profileSubmitting}
        destroyOnHidden
        maskClosable={false}
        afterOpenChange={(open) => {
          if (open) {
            schedulePasswordFieldClear();
          }
        }}
      >
        <Form
          form={profileForm}
          layout="vertical"
          disabled={profileLoading || profileSubmitting}
          autoComplete="off"
        >
          <Form.Item name="username" label={t("profile.username")}>
            <Input disabled autoComplete="username" />
          </Form.Item>
          <Form.Item
            name="displayName"
            label={t("profile.nickname")}
            rules={[
              {
                max: PROFILE_NICKNAME_MAX_LENGTH,
                message: t("profile.nicknameMax", { max: PROFILE_NICKNAME_MAX_LENGTH }),
              },
            ]}
          >
            <Input
              placeholder={t("profile.pleaseInputNickname")}
              autoComplete="nickname"
              maxLength={PROFILE_NICKNAME_MAX_LENGTH}
              showCount
            />
          </Form.Item>
          <Form.Item
            name="email"
            label={t("profile.email")}
            rules={[
              { type: "email", message: t("profile.invalidEmail") },
              {
                max: PROFILE_EMAIL_MAX_LENGTH,
                message: t("profile.emailMax", { max: PROFILE_EMAIL_MAX_LENGTH }),
              },
            ]}
          >
            <Input
              placeholder={t("profile.pleaseInputEmail")}
              autoComplete="email"
              maxLength={PROFILE_EMAIL_MAX_LENGTH}
              showCount
            />
          </Form.Item>
          <Form.Item
            name="phone"
            label={t("profile.phone")}
            rules={[phoneRule]}
          >
            <Input
              placeholder={t("profile.pleaseInputPhone")}
              autoComplete="tel"
              inputMode="numeric"
              maxLength={PROFILE_PHONE_MAX_LENGTH}
              showCount
            />
          </Form.Item>
          <Form.Item
            name="remark"
            label={t("profile.description")}
            rules={[
              {
                max: PROFILE_DESCRIPTION_MAX_LENGTH,
                message: t("profile.descriptionMax", { max: PROFILE_DESCRIPTION_MAX_LENGTH }),
              },
            ]}
          >
            <Input.TextArea
              placeholder={t("profile.pleaseInputDescription")}
              maxLength={PROFILE_DESCRIPTION_MAX_LENGTH}
              showCount
            />
          </Form.Item>
          <Form.Item name="roleName" label={t("profile.role")}>
            <Input disabled />
          </Form.Item>
          <Form.Item name="status" label={t("profile.status")}>
            <Input disabled />
          </Form.Item>
          <Form.Item
            name="currentPassword"
            label={t("profile.currentPassword")}
            rules={[currentPasswordRule]}
          >
            <Input.Password
              placeholder={t("profile.pleaseInputCurrentPassword")}
              autoComplete="new-password"
              name="profile-current-password"
              maxLength={PROFILE_PASSWORD_MAX_LENGTH}
              showCount
            />
          </Form.Item>
          <Form.Item
            name="newPassword"
            label={t("profile.newPassword")}
            dependencies={["currentPassword", "confirmPassword"]}
            rules={[passwordRequiredRule]}
          >
            <Input.Password
              placeholder={t("profile.pleaseInputNewPassword")}
              autoComplete="new-password"
              name="profile-new-password"
              maxLength={PROFILE_PASSWORD_MAX_LENGTH}
              showCount
            />
          </Form.Item>
          <Form.Item
            name="confirmPassword"
            label={t("profile.confirmNewPassword")}
            dependencies={["currentPassword", "newPassword"]}
            rules={[confirmPasswordRule]}
          >
            <Input.Password
              placeholder={t("profile.pleaseInputConfirmPassword")}
              autoComplete="new-password"
              name="profile-confirm-password"
              maxLength={PROFILE_PASSWORD_MAX_LENGTH}
              showCount
            />
          </Form.Item>
        </Form>
      </Modal>
      <UserAgreementConsentModal
        open={needsConsent}
        onAccepted={markAccepted}
      />
    </Layout>
  );
}
