import { Modal, Form, Input, Select, message } from "antd";
import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { createUserApi, createRoleApi } from "@/modules/signin/utils/request";
import {
  passwordRules,
  USERNAME_MAX_LENGTH,
  usernameRules,
} from "@/modules/signin/utils/formRules";
import type { UserItem, RoleItem } from "@/api/generated/auth-client";

const EMAIL_MAX_LENGTH = 30;
const PASSWORD_MAX_LENGTH = 32;

type EditableUserItem = UserItem & {
  is_bootstrap_admin?: boolean;
};

interface CreateUserModalProps {
  visible: boolean;
  editingUser?: EditableUserItem | null;
  onCancel: () => void;
  onSuccess: () => void;
}

const CreateUserModal = ({ visible, editingUser, onCancel, onSuccess }: CreateUserModalProps) => {
  const { t } = useTranslation();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [roles, setRoles] = useState<RoleItem[]>([]);

  const fetchRoles = async () => {
    try {
      const api = createRoleApi();
      const res = await api.listRolesApiAuthserviceRoleGet();
      const resData = res.data as any;
      const roleList = resData.data || resData || [];
      setRoles(roleList);

      if (!editingUser) {
        const currentRole = form.getFieldValue("role");
        const hasMatchedRole = roleList.some((role: RoleItem) => role.id === currentRole);

        if (!hasMatchedRole) {
          const defaultRole =
            roleList.find((role: RoleItem) => role.name?.toLowerCase() === "user") || roleList[0];

          if (defaultRole?.id) {
            form.setFieldValue("role", defaultRole.id);
          }
        }
      }
    } catch (error) {
      console.error("Failed to fetch roles:", error);
    }
  };

  useEffect(() => {
    if (visible) {
      fetchRoles();
      if (editingUser) {
        form.setFieldsValue({
          username: editingUser.username,
          email: editingUser.email,
          role: editingUser.role_id,
        });
      } else {
        form.resetFields();
      }
    }
  }, [visible, editingUser, form]);

  const onFinish = async (values: any) => {
    if (editingUser?.is_bootstrap_admin) {
      message.warning(t("admin.bootstrapAdminRoleLocked"));
      return;
    }

    setLoading(true);
    try {
      const userApi = createUserApi();
      if (editingUser) {
        await userApi.setUserRoleApiAuthserviceUserUserIdPatch({
          userId: editingUser.user_id,
          userRoleBody: { role_id: values.role },
        });
        message.success(t("admin.updateUserRoleSuccess"));
      } else {
        await userApi.createUserApiAuthserviceUserPost({
          createUserBody: {
            username: values.username,
            password: values.password,
            role_id: values.role,
            ...(values.email ? { email: values.email } : {}),
          },
        });
        message.success(t("admin.createUserSuccess"));
      }
      onSuccess();
    } catch (error: any) {
      console.error("Operation failed:", error);
      if (!error?.response && !error?.request) {
        message.error(getLocalizedErrorMessage(error));
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal
      title={editingUser ? t("admin.editUserRole") : t("admin.createUser")}
      open={visible}
      onCancel={onCancel}
      onOk={() => form.submit()}
      confirmLoading={loading}
      okButtonProps={{ disabled: !!editingUser?.is_bootstrap_admin }}
      destroyOnHidden
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={onFinish}
      >
        <Form.Item
          name="username"
          label={t("admin.username")}
          rules={usernameRules}
        >
          <Input
            placeholder={t("admin.enterUsernameWithMax", { max: USERNAME_MAX_LENGTH })}
            disabled={!!editingUser}
            maxLength={USERNAME_MAX_LENGTH}
            showCount
            autoComplete="username"
          />
        </Form.Item>

        {!editingUser && (
          <>
            <Form.Item
              name="email"
              label={t("admin.email")}
              rules={[
                { type: "email", message: t("admin.enterValidEmail") },
                { max: EMAIL_MAX_LENGTH, message: t("admin.emailMax", { max: EMAIL_MAX_LENGTH }) },
              ]}
            >
              <Input
                placeholder={t("admin.enterEmailWithMax", { max: EMAIL_MAX_LENGTH })}
                maxLength={EMAIL_MAX_LENGTH}
                showCount
                autoComplete="email"
              />
            </Form.Item>

            <Form.Item
              name="password"
              label={t("auth.password")}
              rules={passwordRules}
            >
              <Input.Password
                placeholder={t("auth.pleaseInputPasswordSet")}
                maxLength={PASSWORD_MAX_LENGTH}
                autoComplete="new-password"
              />
            </Form.Item>

            <Form.Item
              name="confirmPassword"
              label={t("auth.confirmPassword")}
              dependencies={["password"]}
              hasFeedback
              rules={[
                { required: true, message: t("admin.confirmPasswordRequired") },
                ({ getFieldValue }) => ({
                  validator(_, value) {
                    if (!value || getFieldValue("password") === value) {
                      return Promise.resolve();
                    }
                    return Promise.reject(new Error(t("auth.passwordNotMatch")));
                  },
                }),
              ]}
            >
              <Input.Password
                placeholder={t("admin.confirmPasswordWithMax", { max: PASSWORD_MAX_LENGTH })}
                maxLength={PASSWORD_MAX_LENGTH}
                autoComplete="new-password"
              />
            </Form.Item>
          </>
        )}

        <Form.Item
          name="role"
          label={t("admin.role")}
          rules={[{ required: true, message: t("admin.selectRoleRequired") }]}
        >
          <Select
            placeholder={t("admin.selectRole")}
            disabled={!!editingUser?.is_bootstrap_admin}
          >
            {roles.map((role: any) => (
              <Select.Option key={role.id} value={role.id}>
                {role.name}
              </Select.Option>
            ))}
          </Select>
        </Form.Item>
      </Form>
    </Modal>
  );
};

export default CreateUserModal;
