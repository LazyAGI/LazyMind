import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Button, Form, Input, Layout, Modal, Popover, message } from "antd";
import {
  CodeOutlined,
  SettingOutlined,
  SearchOutlined,
  AppstoreOutlined,
  DatabaseOutlined,
  ApiOutlined,
  UserOutlined,
  TeamOutlined,
  GlobalOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  PlusOutlined,
  RightOutlined,
  FolderOpenOutlined,
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
  isDeveloperModeActive,
  setDeveloperModeActive,
} from "@/utils/developerMode";
import RecordList from "@/modules/chat/components/RecordList";
import {
  CHAT_RESUME_CONVERSATION_KEY,
  CHAT_SELECT_CONVERSATION_EVENT,
} from "@/modules/chat/constants/chat";
import "./index.scss";

const { Content, Sider } = Layout;
const MAINLAND_CHINA_PHONE_REGEX = /^1[3-9]\d{9}$/;
const MAIN_MENU_COLLAPSED_STORAGE_KEY = "lazymind:main-menu-collapsed";
const MAIN_MENU_TRANSITION_MS = 240;

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
  const [sidebarSearchText, setSidebarSearchText] = useState("");
  const [isMenuCollapsed, setIsMenuCollapsed] = useState(readStoredMainMenuCollapsed);
  const [shouldRenderMenuContent, setShouldRenderMenuContent] = useState(
    () => !readStoredMainMenuCollapsed(),
  );
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const [profileDetail, setProfileDetail] = useState<UserDetailResponse | null>(null);

  const pathname = location.pathname || "/agent/chat";

  const settingsMenuItems = [
    {
      key: "/admin",
      label: t("layout.systemManagement"),
      icon: <TeamOutlined className="settings-popover-icon" />,
    },
    ...(isAdminUser
      ? [
          {
            key: "developer-toggle",
            label: t("layout.developer"),
            icon: <CodeOutlined className="settings-popover-icon" />,
          },
        ]
      : []),
  ];
  const resourceNavItems = [
    {
      key: "/lib/knowledge",
      label: t("layout.knowledgeBase"),
      icon: <AppstoreOutlined />,
    },
    {
      key: "/data-sources",
      label: t("layout.dataSourceManagement"),
      icon: <DatabaseOutlined />,
    },
    {
      key: "/model-providers",
      label: t("layout.modelProviderManagement"),
      icon: <ApiOutlined />,
    },
  ];
  const aiEvolutionNavItems = [
    {
      key: "/memory-management",
      label: t("layout.memoryManagement"),
      icon: <AppstoreOutlined />,
    },
    ...(isAdminUser && developerActive
      ? [
          {
            key: "/self-evolution",
            label: t("layout.selfEvolution"),
            icon: <CodeOutlined />,
          },
        ]
      : []),
  ];
  const logoSrc =
    (import.meta.env as ImportMetaEnv & { VITE_APP_LOGO?: string })
      .VITE_APP_LOGO || "";
  const needsRestoreButtonSafeArea =
    pathname.startsWith("/model-providers") ||
    pathname.startsWith("/memory-management") ||
    pathname.startsWith("/self-evolution");
  const contentClassName = [
    "main-layout-content",
    isMenuCollapsed ? "is-sidebar-collapsed" : "",
    isMenuCollapsed && needsRestoreButtonSafeArea ? "is-restore-safe-area-page" : "",
  ]
    .filter(Boolean)
    .join(" ");

  useEffect(() => {
    setDeveloperActive(isDeveloperModeActive());
  }, []);

  const refreshLayoutUser = useCallback(async () => {
    if (!AgentAppsAuth.isLoggedIn()) {
      setUserInfo(AgentAppsAuth.getUserInfo());
      return;
    }

    try {
      await fetchCurrentUser();
    } catch (error) {
      console.error("Failed to refresh current user:", error);
    } finally {
      setUserInfo(AgentAppsAuth.getUserInfo());
    }
  }, []);

  useEffect(() => {
    refreshLayoutUser();

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

    document.addEventListener("visibilitychange", handleVisibilityChange);
    window.addEventListener("focus", handleFocus);
    window.addEventListener("storage", handleStorage);
    window.addEventListener(AUTH_USER_CHANGE_EVENT, handleUserChange);

    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.removeEventListener("focus", handleFocus);
      window.removeEventListener("storage", handleStorage);
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, handleUserChange);
    };
  }, [refreshLayoutUser]);

  useEffect(() => {
    if (!isAdminUser && developerActive) {
      setDeveloperActive(false);
      setDeveloperModeActive(false);
    }
  }, [developerActive, isAdminUser]);

  useEffect(() => {
    if (pathname.startsWith("/self-evolution") && (!isAdminUser || !developerActive)) {
      navigate("/agent/chat", { replace: true });
    }
  }, [pathname, isAdminUser, developerActive, navigate]);

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

  const emitConversationSelection = (conversationId: string) => {
    window.dispatchEvent(
      new CustomEvent(CHAT_SELECT_CONVERSATION_EVENT, {
        detail: { conversationId, source: "sidebar" },
      }),
    );
  };

  const handleNewChat = () => {
    try {
      sessionStorage.removeItem(CHAT_RESUME_CONVERSATION_KEY);
    } catch {
      // ignore storage errors
    }
    setCurrentSidebarConversationId("");
    emitConversationSelection("");
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

  const handleSettingsNavigate = (targetPath: string) => {
    if (targetPath === "developer-toggle") {
      if (developerActive) {
        setDeveloperActive(false);
        setDeveloperModeActive(false);
        message.success(t("admin.developerDeactivated"));
        if (pathname.startsWith("/self-evolution")) {
          navigate("/agent/chat");
        }
        return;
      }

      setDeveloperActive(true);
      setDeveloperModeActive(true);
      message.success(t("admin.developerActivated"));
      return;
    }

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

  if (!isLoggedIn) {
    return <Navigate to="/login" replace />;
  }

  return (
    <Layout hasSider className="main-layout">
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
              onClick={handleNewChat}
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
              aria-label={isMenuCollapsed ? "展开菜单" : "收起菜单"}
              title={isMenuCollapsed ? "展开菜单" : "收起菜单"}
            >
              {isMenuCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            </button>
          </div>
          {shouldRenderMenuContent ? (
            <>
              <div className="sider-primary-action">
                <Button
                  type="text"
                  className="sider-new-chat-button"
                  icon={<PlusOutlined />}
                  onClick={handleNewChat}
                >
                  {t("layout.newChat")}
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
                  content={renderModulePopover(aiEvolutionNavItems)}
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
            <div className="bottom-item language-item">
              <GlobalOutlined className="bottom-icon" />
              {shouldRenderMenuContent && <LanguageSwitcher />}
            </div>
            <Popover
              content={
                <div className="settings-popover">
                  {settingsMenuItems.map((item) => (
                    <Button
                      key={item.key}
                      type="text"
                      className={`settings-popover-button${
                        item.key === "developer-toggle" && developerActive ? " is-active" : ""
                      }`}
                      onClick={() => handleSettingsNavigate(item.key)}
                    >
                      {item.icon}
                      <span>{item.label}</span>
                      {item.key === "developer-toggle" && developerActive && (
                        <span className="settings-active-badge">{t("admin.developerActiveTag")}</span>
                      )}
                    </Button>
                  ))}
                  {isLoggedIn ? (
                    <Button
                      type="text"
                      className="settings-popover-button"
                      onClick={handleLogout}
                    >
                      <span>{t("layout.logout")}</span>
                    </Button>
                  ) : (
                    <Button
                      type="text"
                      className="settings-popover-button"
                      onClick={handleGoLogin}
                    >
                      <span>{t("layout.goLogin")}</span>
                    </Button>
                  )}
                </div>
              }
              arrow={false}
              placement="top"
              trigger="click"
              open={settingsOpen}
              onOpenChange={setSettingsOpen}
            >
              <div
                className="bottom-item settings-trigger"
                role="button"
                tabIndex={0}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    setSettingsOpen((open) => !open);
                  }
                }}
              >
                <SettingOutlined className="bottom-icon" />
                {shouldRenderMenuContent && <span className="bottom-text">{t("layout.settings")}</span>}
              </div>
            </Popover>
            {userName && (
              <div
                className="bottom-item user-item"
                onClick={handleOpenProfile}
                role="button"
                tabIndex={0}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    handleOpenProfile();
                  }
                }}
              >
                <UserOutlined className="bottom-icon" />
                {shouldRenderMenuContent && <span className="bottom-text">{userName}</span>}
              </div>
            )}
          </div>
        </div>
      </Sider>
      <Layout className={contentClassName}>
        <Content className="main-layout-body">
          {isMenuCollapsed ? (
            <button
              type="button"
              className="main-menu-restore-button"
              onClick={toggleMenu}
              aria-label="展开菜单"
              title="展开菜单"
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
          <Form.Item name="displayName" label={t("profile.nickname")}>
            <Input placeholder={t("profile.pleaseInputNickname")} autoComplete="nickname" />
          </Form.Item>
          <Form.Item
            name="email"
            label={t("profile.email")}
            rules={[{ type: "email", message: t("profile.invalidEmail") }]}
          >
            <Input placeholder={t("profile.pleaseInputEmail")} autoComplete="email" />
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
              maxLength={11}
            />
          </Form.Item>
          <Form.Item name="remark" label={t("profile.description")}>
            <Input.TextArea placeholder={t("profile.pleaseInputDescription")} />
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
            />
          </Form.Item>
        </Form>
      </Modal>
    </Layout>
  );
}
