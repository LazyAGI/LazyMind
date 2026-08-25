import { useCallback, useEffect, useState } from "react";
import type { ReactNode, WheelEvent as ReactWheelEvent } from "react";
import { Button, Form, Input, Layout, Modal, Popover, Spin, message } from "antd";
import {
  CodeOutlined,
  SettingOutlined,
  SearchOutlined,
  AppstoreOutlined,
  DatabaseOutlined,
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
import { Navigate, Outlet, useLocation, useNavigate } from "react-router-dom";
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
import {
	DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
  persistDeveloperModeActive,
  syncDeveloperModeFromServer,
} from "@/utils/developerMode";
import RecordList from "@/modules/chat/components/RecordList";
import {
  CHAT_CONVERSATION_FILTER_EVENT,
  CHAT_CONVERSATION_FILTER_KEY,
  type ChatConversationFilter,
  CHAT_NEW_RUN_IN_BACKGROUND_KEY,
  CHAT_RESUME_CONVERSATION_KEY,
  CHAT_SELECT_CONVERSATION_EVENT,
  selectChatConversationFilter,
} from "@/modules/chat/constants/chat";
import { runtimeFeatures } from "@/runtime/features";
import { shouldHideLocalUserControls } from "@/runtime/localSession";
import { useLocalSessionGate } from "@/runtime/useLocalSessionGate";
import UserAgreementConsentModal, {
  useUserAgreementConsentGate,
} from "@/components/UserAgreementConsentModal";
import TerminalConnectionQuickPanel from "@/modules/channelGateway/components/TerminalConnectionQuickPanel";
import "./index.scss";

const { Content, Sider } = Layout;
const MAINLAND_CHINA_PHONE_REGEX = /^1[3-9]\d{9}$/;
const MAIN_MENU_COLLAPSED_STORAGE_KEY = "lazymind:main-menu-collapsed";
const MAIN_MENU_TRANSITION_MS = 240;
const PROFILE_NICKNAME_MAX_LENGTH = 50;
const PROFILE_EMAIL_MAX_LENGTH = 30;
const PROFILE_PHONE_MAX_LENGTH = 11;
const PROFILE_DESCRIPTION_MAX_LENGTH = 200;
const PROFILE_PASSWORD_MAX_LENGTH = 32;

function readStoredMainMenuCollapsed() {
  try {
    return localStorage.getItem(MAIN_MENU_COLLAPSED_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

function isAdminRole(role?: string) {
  const normalizedRole = (role || "").trim().toLowerCase();
  return (
    normalizedRole === "admin" ||
    normalizedRole === "system-admin" ||
    normalizedRole === "system_admin" ||
    normalizedRole.endsWith(".admin")
  );
}

function canScrollVertically(element: HTMLElement, deltaY: number) {
  const style = window.getComputedStyle(element);
  if (style.overflowY !== "auto" && style.overflowY !== "scroll") {
    return false;
  }

  const maxScrollTop = element.scrollHeight - element.clientHeight;
  if (maxScrollTop <= 1) {
    return false;
  }

  return deltaY < 0 ? element.scrollTop > 0 : element.scrollTop < maxScrollTop;
}

function readChatConversationMode(): ChatConversationFilter {
  try {
    return sessionStorage.getItem(CHAT_CONVERSATION_FILTER_KEY) === "task"
      ? "task"
      : "normal";
  } catch {
    return "normal";
  }
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

  const [userInfo, setUserInfo] = useState(() => AgentAppsAuth.getUserInfo());
  const isLoggedIn = Boolean(userInfo?.token);
  const userName = userInfo?.username || "";
  const isAdminUser = isAdminRole(userInfo?.role);
  const hideLocalUserControls = shouldHideLocalUserControls();
  const accountDisplayName = userInfo?.displayName || userName || "LazyMind";
  const accountRoleLabel = isAdminUser
    ? t("layout.systemAdministrator")
    : t("layout.normalUser");

  const [currentSidebarConversationId, setCurrentSidebarConversationId] =
    useState(() => {
      try {
        return sessionStorage.getItem(CHAT_RESUME_CONVERSATION_KEY) || "";
      } catch {
        return "";
      }
    });
  const [profileModalOpen, setProfileModalOpen] = useState(false);
  const [profileLoading, setProfileLoading] = useState(false);
  const [profileSubmitting, setProfileSubmitting] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [terminalConnectionOpen, setTerminalConnectionOpen] = useState(false);
  const [sidebarSearchText, setSidebarSearchText] = useState("");
  const [chatConversationMode, setChatConversationMode] =
    useState<ChatConversationFilter>(readChatConversationMode);
  const [isMenuCollapsed, setIsMenuCollapsed] = useState(readStoredMainMenuCollapsed);
  const [shouldRenderMenuContent, setShouldRenderMenuContent] = useState(
    () => !readStoredMainMenuCollapsed(),
  );
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const [profileDetail, setProfileDetail] = useState<UserDetailResponse | null>(null);

  const pathname = location.pathname || "/agent/chat";

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
    ...(isAdminUser && !runtimeFeatures.hideEvo
      ? [
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
        ]
      : []),
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
      key: "/dataset-management",
      label: t("layout.datasetManagement"),
      icon: <DatabaseOutlined />,
    },
    {
      key: "/cloud-documents",
      label: t("layout.cloudDocuments"),
      icon: <CloudOutlined />,
    },
    {
      key: "/databases",
      label: t("layout.database"),
      icon: <DatabaseOutlined />,
    },
  ];
  const hideEvo = runtimeFeatures.hideEvo;
  const canAccessSelfEvolution = !hideEvo && developerActive && isAdminUser;
  const logoSrc =
    (import.meta.env as ImportMetaEnv & { VITE_APP_LOGO?: string })
      .VITE_APP_LOGO || "";
  const needsRestoreButtonSafeArea =
    pathname.startsWith("/cloud-documents") ||
    pathname.startsWith("/channels") ||
    pathname.startsWith("/settings") ||
    pathname.startsWith("/lib/knowledge/detail") ||
    pathname.startsWith("/memory-management") ||
    pathname.startsWith("/self-evolution");
  const isSelfEvolutionObservationPage =
    pathname.startsWith("/self-evolution/detail/") && pathname.includes("/observation/");
  const isChatPage = pathname.startsWith("/agent/chat");
  const contentClassName = [
    "main-layout-content",
    isChatPage ? "is-chat-page" : "",
    isMenuCollapsed ? "is-sidebar-collapsed" : "",
    isMenuCollapsed && needsRestoreButtonSafeArea ? "is-restore-safe-area-page" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const handleChatWheel = useCallback(
    (event: ReactWheelEvent<HTMLDivElement>) => {
      if (!isChatPage || event.deltaY === 0) {
        return;
      }

      const target = event.target instanceof HTMLElement ? event.target : null;
      const messageContainer = event.currentTarget.querySelector<HTMLElement>(
        ".message-container",
      );
      if (!target || !messageContainer) {
        return;
      }

      // The Markdown document editor owns wheel input, including when it has
      // reached the top or bottom of its own scroll area.
      if (target.closest(".writer-markdown-editor")) {
        return;
      }

      let ancestor: HTMLElement | null = target;
      while (ancestor && ancestor !== event.currentTarget) {
        if (canScrollVertically(ancestor, event.deltaY)) {
          return;
        }
        ancestor = ancestor.parentElement;
      }

      // The message list already handles its own wheel events, including
      // nested scrollable blocks such as long thinking text.
      if (messageContainer.contains(target)) {
        return;
      }

      messageContainer.scrollBy({ top: event.deltaY, behavior: "auto" });
    },
    [isChatPage],
  );

  const refreshLayoutUser = useCallback(async () => {
    if (!AgentAppsAuth.isLoggedIn()) {
      setUserInfo(AgentAppsAuth.getUserInfo());
      return;
    }

    try {
      await fetchCurrentUser();
      const devActive = await syncDeveloperModeFromServer();
      setDeveloperActive(devActive);
    } catch (error) {
      console.error("Failed to refresh current user:", error);
    } finally {
      setUserInfo(AgentAppsAuth.getUserInfo());
    }
  }, []);
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

    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        refreshLayoutUser();
      }
    };
    const handleFocus = () => {
      refreshLayoutUser();
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
  }, [localSessionGate.enabled, refreshLayoutUser]);

  useEffect(() => {
    if (!isAdminUser && developerActive) {
      setDeveloperActive(false);
      void persistDeveloperModeActive(false);
    }
  }, [developerActive, isAdminUser]);

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
    if (!isMenuCollapsed) {
      setShouldRenderMenuContent(true);
      return;
    }

    const timer = window.setTimeout(() => {
      setShouldRenderMenuContent(false);
    }, MAIN_MENU_TRANSITION_MS);

    return () => {
      window.clearTimeout(timer);
    };
  }, [isMenuCollapsed]);

  useEffect(() => {
    setIsMenuCollapsed(readStoredMainMenuCollapsed());
  }, []);

  useEffect(() => {
    try {
      localStorage.setItem(MAIN_MENU_COLLAPSED_STORAGE_KEY, isMenuCollapsed ? "1" : "0");
    } catch {
      // ignore persistence errors
    }
  }, [isMenuCollapsed]);

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
    const handleConversationSelect = (event: Event) => {
      const conversationId =
        (event as CustomEvent<{ conversationId?: string }>).detail
          ?.conversationId || "";
      setCurrentSidebarConversationId(conversationId);
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
  }, []);

  const toggleMenu = () => {
    setIsMenuCollapsed((prev) => !prev);
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
    selectChatConversationFilter(runInBackground ? "task" : "normal");
    try {
      sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
      sessionStorage.setItem(
        CHAT_NEW_RUN_IN_BACKGROUND_KEY,
        runInBackground ? "1" : "0",
      );
    } catch {
      // ignore storage errors
    }
    setCurrentSidebarConversationId("");
    emitConversationSelection("", runInBackground);
    navigate("/agent/chat/home");
  };

  const handleSidebarConversationSelected = (conversation: Conversation) => {
    const conversationId = conversation.conversation_id || "";
    if (!conversationId) {
      return;
    }
    try {
      sessionStorage.setItem(CHAT_RESUME_CONVERSATION_KEY, conversationId);
    } catch {
      // ignore storage errors
    }
    setCurrentSidebarConversationId(conversationId);
    emitConversationSelection(conversationId);
    navigate("/agent/chat/home");
  };

  const handleSidebarConversationRemoved = (conversation: Conversation) => {
    const conversationId = conversation.conversation_id || "";
    if (!conversationId || conversationId !== currentSidebarConversationId) {
      return;
    }
    try {
      sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
    } catch {
      // ignore storage errors
    }
    setCurrentSidebarConversationId("");
    emitConversationSelection("");
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
    return <Navigate to="/login" replace />;
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
    <Layout hasSider className="main-layout" onWheelCapture={handleChatWheel}>
      <Sider
        width={252}
        collapsedWidth={0}
        collapsible
        trigger={null}
        collapsed={isMenuCollapsed}
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
              {logoSrc ? (
                <img src={logoSrc} alt="logo" />
              ) : (
                <img src={logoImage} alt="logo" />
              )}
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
          {shouldRenderMenuContent ? (
            <>
              <div className="sider-primary-action">
                <Button
                  type="text"
                  className={`sider-new-chat-button${!isTaskMode ? " is-active" : ""}`}
                  icon={<PlusOutlined />}
                  onClick={() => handleNewChat(false)}
                  aria-pressed={!isTaskMode}
                >
                  {t("layout.newChat")}
                </Button>
                <Button
                  type="text"
                  className={`sider-new-chat-button${isTaskMode ? " is-active" : ""}`}
                  icon={<PlusOutlined />}
                  onClick={() => handleNewChat(true)}
                  aria-pressed={isTaskMode}
                >
                  {t("layout.newTask")}
                </Button>
              </div>
              <div className="sider-module-actions">
                <Popover
                  content={renderModulePopover(resourceNavItems)}
                  arrow={false}
                  placement="rightTop"
                  trigger="hover"
                  mouseLeaveDelay={0.25}
                  align={{ offset: [-4, 0] }}
                  overlayClassName="sider-module-overlay"
                >
                  <button type="button" className="sider-module-trigger">
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
                  trigger="hover"
                  mouseLeaveDelay={0.25}
                  align={{ offset: [-4, 0] }}
                  overlayClassName="sider-module-overlay"
                >
                  <button type="button" className="sider-module-trigger">
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
                >
                  <span className="sider-module-icon">
                    <UnorderedListOutlined />
                  </span>
                  <span className="sider-module-text">{t("layout.taskCenter")}</span>
                </button>
              </div>
              <div className="sider-history-search">
                <Input
                  className="sider-history-search-input"
                  type="search"
                  prefix={<SearchOutlined />}
                  allowClear
                  value={sidebarSearchText}
                  placeholder={t("chat.searchConversation")}
                  aria-label={t("chat.searchConversation")}
                  onChange={(event) => setSidebarSearchText(event.target.value)}
                />
              </div>
            </>
          ) : null}
          {shouldRenderMenuContent && (
            <div className="sider-history">
              <RecordList
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
          )}
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
                >
                  <span className="sider-account-avatar" aria-hidden="true">
                    <UserOutlined />
                  </span>
                  {shouldRenderMenuContent && (
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
      </Sider>
      <Layout className={contentClassName}>
        <Content className="main-layout-body">
          {isMenuCollapsed && !isSelfEvolutionObservationPage ? (
            <button
              type="button"
              className="main-menu-restore-button"
              onClick={toggleMenu}
              aria-label={t("layout.expandMenu")}
              title={t("layout.expandMenu")}
            >
              <MenuUnfoldOutlined />
            </button>
          ) : null}
          <div className="sub-app-container">
            <Outlet
              context={{
                isMenuCollapsed,
                toggleMenu,
              }}
            />
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
