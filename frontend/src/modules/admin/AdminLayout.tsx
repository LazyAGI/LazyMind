import {
  LeftCircleOutlined,
  UserOutlined,
  UsergroupAddOutlined,
} from "@ant-design/icons";
import { Avatar, Layout, Menu } from "antd";
import type { MenuProps } from "antd";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import logoImage from "@/public/Lazy.png";
import "./index.scss";

const { Content, Sider } = Layout;

type MenuItem = Required<MenuProps>["items"][number];

function isAdminRole(role?: string) {
  const normalizedRole = (role || "").trim().toLowerCase();
  return (
    normalizedRole === "admin" ||
    normalizedRole === "system-admin" ||
    normalizedRole === "system_admin" ||
    normalizedRole.endsWith(".admin")
  );
}

export default function AdminLayout() {
  const location = useLocation();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const userInfo = AgentAppsAuth.getUserInfo();
  const isLoggedIn = Boolean(userInfo?.token);
  const isAdminUser = isAdminRole(userInfo?.role);

  const pathname = location.pathname;
  const selectedKey = pathname.startsWith("/admin/users")
    ? "/admin/users"
    : "/admin/groups";

  const menuChildren: MenuItem[] = [
    ...(isAdminUser
      ? [
          {
            key: "/admin/users",
            label: t("layout.userManagement"),
            icon: <UserOutlined />,
          },
        ]
      : []),
    {
      key: "/admin/groups",
      label: t("layout.groupManagement"),
      icon: <UsergroupAddOutlined />,
    },
  ];

  const menuItems: MenuItem[] = [
    {
      key: "system",
      label: t("layout.systemManagement"),
      type: "group",
      children: menuChildren,
    },
  ];
  const logoSrc =
    (import.meta.env as ImportMetaEnv & { VITE_APP_LOGO?: string })
      .VITE_APP_LOGO || "";

  const onMenuClick: MenuProps["onClick"] = ({ key }) => {
    if (String(key).startsWith("/admin/")) {
      navigate(String(key));
    }
  };

  if (!isLoggedIn) {
    return <Navigate to="/login" replace />;
  }

  if (
    !isAdminUser &&
    pathname.startsWith("/admin/users")
  ) {
    return <Navigate to="/admin/groups" replace />;
  }

  return (
    <Layout className="admin-layout">
      <Sider
        width={232}
        className="admin-layout-sider"
      >
        <div className="admin-layout-brand">
          <img
            src={logoSrc || logoImage}
            alt="logo"
            className="admin-layout-brand-logo"
          />
        </div>
        <Menu
          mode="inline"
          selectedKeys={[selectedKey]}
          items={menuItems}
          className="admin-layout-menu"
          onClick={onMenuClick}
        />
        <div className="admin-layout-footer">
          <div className="admin-layout-footer-user">
            <Avatar size={24} icon={<UserOutlined />} />
            <span>{userInfo?.username || "user"}</span>
          </div>
          <button
            type="button"
            className="admin-layout-footer-back"
            onClick={() => navigate("/agent/chat")}
          >
            <LeftCircleOutlined />
            <span>{t("admin.backToApp")}</span>
          </button>
        </div>
      </Sider>
      <Layout className="admin-layout-content">
        <Content className="admin-layout-body">
          <div className="admin-layout-panel">
            <Outlet />
          </div>
        </Content>
      </Layout>
    </Layout>
  );
}
