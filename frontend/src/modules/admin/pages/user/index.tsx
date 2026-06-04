import { useState, useEffect, useCallback } from "react";
import { Table, Button, Space, Tag, Popconfirm, message, Modal, Form, Input, Tooltip } from "antd";
import {
  PlusOutlined,
  StopOutlined,
  CheckCircleOutlined,
  EditOutlined,
  KeyOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import CreateUserModal from "./components/CreateUserModal";
import { createUserApi } from "@/modules/signin/utils/request";
import { validatePassword } from "@/modules/signin/utils/formRules";
import type { UserItem } from "@/api/generated/auth-client";
import { getLocalizedTablePagination } from "@/components/ui/pagination";

const PASSWORD_MAX_LENGTH = 32;
const USERNAME_COLUMN_WIDTH = 220;

type AdminUserItem = UserItem & {
  is_bootstrap_admin?: boolean;
};

type RawUserItem = Partial<AdminUserItem> & {
  id?: string | number;
  userId?: string | number;
  roleId?: string | number;
  roleName?: string;
  status_text?: string;
  disabled?: boolean;
  role?: string | { id?: string | number; name?: string };
};

const resolveUserId = (user?: RawUserItem | null) => {
  const candidate = user?.user_id ?? user?.userId ?? user?.id;
  if (candidate === undefined || candidate === null || candidate === "") {
    return "";
  }
  return String(candidate);
};

const normalizeUserItem = (user: RawUserItem): AdminUserItem => {
  const role =
    user.role && typeof user.role === "object" ? user.role : undefined;
  const statusFromDisabled =
    typeof user.disabled === "boolean"
      ? user.disabled
        ? "disabled"
        : "active"
      : undefined;

  return {
    ...user,
    user_id: resolveUserId(user),
    role_id: String(user.role_id ?? user.roleId ?? role?.id ?? ""),
    role_name: user.role_name ?? user.roleName ?? role?.name ?? String(user.role ?? ""),
    status: user.status ?? user.status_text ?? statusFromDisabled ?? "active",
  } as AdminUserItem;
};

const UserManagement = () => {
  const { t } = useTranslation();
  const [isModalVisible, setIsModalVisible] = useState(false);
  const [loading, setLoading] = useState(false);
  const [users, setUsers] = useState<AdminUserItem[]>([]);
  const [pagination, setPagination] = useState({ current: 1, pageSize: 20, total: 0 });
  const [editingUser, setEditingUser] = useState<AdminUserItem | null>(null);
  const [resetPasswordForm] = Form.useForm();
  const [searchTerm, setSearchTerm] = useState("");

  const fetchUsers = useCallback(async (page = 1, pageSize = 20, search = "") => {
    setLoading(true);
    try {
      const api = createUserApi();
      const res = await api.listUsersApiAuthserviceUserGet({
        page,
        pageSize,
        search: search || undefined,
      });
      const resData = res.data as any;
      const data = resData.data || resData;
      const rawUsers = Array.isArray(data?.users)
        ? data.users
        : Array.isArray(data)
          ? data
          : [];
      const normalizedUsers = rawUsers.map((item: RawUserItem) =>
        normalizeUserItem(item),
      );

      setUsers(normalizedUsers);
      setPagination({
        current: Number(data.page || page),
        pageSize: Number(data.page_size || pageSize),
        total: Number(data.total || normalizedUsers.length || 0),
      });
    } catch (error) {
      console.error("Failed to fetch users:", error);
      message.error(t("admin.fetchUsersFailed"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchUsers(pagination.current, pagination.pageSize, searchTerm);
  }, [fetchUsers]);

  const handleSearch = (value: string) => {
    setSearchTerm(value);
    fetchUsers(1, pagination.pageSize, value);
  };

  const isUserDisabled = (status?: string) => {
    const normalizedStatus = status?.toLowerCase();
    return !["active", "enabled", "normal"].includes(normalizedStatus || "");
  };

  const handleDisable = async (user: RawUserItem) => {
    await handleToggleUserStatus(user, true);
  };

  const handleEnable = async (user: RawUserItem) => {
    await handleToggleUserStatus(user, false);
  };

  const handleToggleUserStatus = async (user: RawUserItem, disabled: boolean) => {
    const userId = resolveUserId(user);

    if (disabled && user.is_bootstrap_admin) {
      message.warning(t("admin.bootstrapAdminDisableLocked"));
      return;
    }

    if (!userId) {
      message.error(
        disabled ? t("admin.disableFailed") : t("admin.enableFailed"),
      );
      console.error("Toggle user status skipped: missing user id", user);
      return;
    }

    try {
      const api = createUserApi();
      await api.disableUserApiAuthserviceUserUserIdDisablePatch({
        userId,
        disableUserBody: {
          disabled,
        },
      });
      message.success(
        disabled ? t("admin.disableSuccess") : t("admin.enableSuccess"),
      );
      fetchUsers(pagination.current, pagination.pageSize, searchTerm);
    } catch (error) {
      console.error("Toggle user status failed:", error);
      message.error(
        disabled ? t("admin.disableFailed") : t("admin.enableFailed"),
      );
    }
  };

  const handleEditRole = (user: AdminUserItem) => {
    setEditingUser(user);
    setIsModalVisible(true);
  };

  const handleResetPassword = (user: UserItem) => {
    const userId = resolveUserId(user);

    Modal.confirm({
      title: t("admin.resetUserPasswordTitle", { username: user.username }),
      content: (
        <Form form={resetPasswordForm} layout="vertical">
          <Form.Item
            name="new_password"
            label={t("admin.newPassword")}
            rules={[
              { required: true, message: t("admin.enterNewPasswordRequired") },
              {
                validator: async (_, value) => validatePassword(value),
              },
            ]}
          >
            <Input.Password
              placeholder={t("admin.enterNewPassword", { max: PASSWORD_MAX_LENGTH })}
              maxLength={PASSWORD_MAX_LENGTH}
              autoComplete="new-password"
            />
          </Form.Item>
        </Form>
      ),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      onOk: async () => {
        try {
          if (!userId) {
            throw new Error("Missing user id");
          }
          const values = await resetPasswordForm.validateFields();
          const api = createUserApi();
          await api.resetPasswordApiAuthserviceUserUserIdResetPasswordPatch({
            userId,
            resetPasswordBody: { new_password: values.new_password },
          });
          message.success(t("admin.resetPasswordSuccess"));
          resetPasswordForm.resetFields();
        } catch (error) {
          console.error("Reset password failed:", error);
          message.error(t("admin.resetPasswordFailed"));
          return Promise.reject();
        }
      },
      onCancel: () => {
        resetPasswordForm.resetFields();
      },
    });
  };

  const columns = [
    {
      title: t("admin.username"),
      dataIndex: "username",
      key: "username",
      width: USERNAME_COLUMN_WIDTH,
      ellipsis: true,
      render: (username: string) => (
        <Tooltip title={username}>
          <span
            style={{
              display: "block",
              width: "100%",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {username}
          </span>
        </Tooltip>
      ),
    },
    {
      title: t("admin.email"),
      dataIndex: "email",
      key: "email",
      width: 180,
      render: (email: string) => email || "-",
    },
    {
      title: t("admin.role"),
      dataIndex: "role_name",
      key: "role_name",
      width: 120,
      render: (roleName: string) => (
        <Tag color={roleName?.toLowerCase().includes("admin") ? "blue" : "green"}>
          {roleName || t("admin.normalUser")}
        </Tag>
      ),
    },
    {
      title: t("admin.status"),
      dataIndex: "status",
      key: "status",
      width: 80,
      render: (status: string) => (
        <Tag color={!isUserDisabled(status) ? "success" : "default"}>
          {!isUserDisabled(status) ? t("admin.normal") : t("admin.disabled")}
        </Tag>
      ),
    },
    {
      title: t("admin.actions"),
      key: "action",
      fixed: 'right' as const,
      width: 240,
      render: (_: any, record: AdminUserItem) => {
        const disabled = isUserDisabled(record.status);
        const isBootstrapAdmin = !!record.is_bootstrap_admin;
        const statusButtonDisabled = isBootstrapAdmin && !disabled;
        const editRoleButton = (
          <Button
            type="link"
            size="small"
            icon={<EditOutlined />}
            disabled={isBootstrapAdmin}
            onClick={() => handleEditRole(record)}
          >
            {t("admin.editUserRole")}
          </Button>
        );

        return (
        <Space size={0}>
          {isBootstrapAdmin ? (
            <Tooltip title={t("admin.bootstrapAdminRoleLocked")}>
              <span>{editRoleButton}</span>
            </Tooltip>
          ) : (
            editRoleButton
          )}
          <Button 
            type="link" 
            size="small"
            icon={<KeyOutlined />} 
            onClick={() => handleResetPassword(record)}
          >
            {t("admin.resetPassword")}
          </Button>
          {statusButtonDisabled ? (
            <Tooltip title={t("admin.bootstrapAdminDisableLocked")}>
              <span>
                <Button
                  type="link"
                  size="small"
                  danger
                  disabled
                  icon={<StopOutlined />}
                >
                  {t("admin.disable")}
                </Button>
              </span>
            </Tooltip>
          ) : (
            <Popconfirm
              title={
                disabled
                  ? t("admin.enableUserConfirm")
                  : t("admin.disableUserConfirm")
              }
              onConfirm={() =>
                disabled
                  ? handleEnable(record)
                  : handleDisable(record)
              }
              okText={t("common.confirm")}
              cancelText={t("common.cancel")}
            >
              <Button
                type="link"
                size="small"
                danger={!disabled}
                icon={disabled ? <CheckCircleOutlined /> : <StopOutlined />}
              >
                {disabled ? t("admin.enable") : t("admin.disable")}
              </Button>
            </Popconfirm>
          )}
        </Space>
        );
      },
    },
  ];

  const handleCreateSuccess = () => {
    setIsModalVisible(false);
    setEditingUser(null);
    fetchUsers(pagination.current, pagination.pageSize, searchTerm);
  };

  const handleTableChange = (newPagination: any) => {
    fetchUsers(newPagination.current, newPagination.pageSize, searchTerm);
  };

  return (
    <div className="admin-page">
      <div className="admin-page-toolbar">
        <div className="admin-page-toolbar-left">
          <h2 className="admin-page-title">{t("admin.userManagement")}</h2>
          <Input.Search
            placeholder={t("admin.searchUsername")}
            allowClear
            onSearch={handleSearch}
            className="admin-page-search"
          />
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          className="admin-page-primary-button"
          onClick={() => {
            setEditingUser(null);
            setIsModalVisible(true);
          }}
        >
          {t("admin.createUser")}
        </Button>
      </div>

      <Table
        className="admin-page-table"
        columns={columns}
        dataSource={users}
        rowKey={(record) => resolveUserId(record) || record.username}
        loading={loading}
        tableLayout="fixed"
        scroll={{ x: 800 }}
        pagination={getLocalizedTablePagination({
          ...pagination,
          showSizeChanger: true,
          showQuickJumper: true,
          showTotal: (total) => t("common.totalItems", { total }),
        }, t)}
        onChange={handleTableChange}
      />

      <CreateUserModal
        visible={isModalVisible}
        editingUser={editingUser}
        onCancel={() => {
          setIsModalVisible(false);
          setEditingUser(null);
        }}
        onSuccess={handleCreateSuccess}
      />
    </div>
  );
};

export default UserManagement;
