import { useEffect } from "react";
import { Form, Input, InputNumber, Modal, Select, Space } from "antd";
import { useTranslation } from "react-i18next";
import {
  type DatabaseConnectionItem,
  type DatabaseConnectionPayload,
} from "../api/databaseConnections";

export type DatabaseConnectionFormValues = DatabaseConnectionPayload & {
  options_text?: string;
};

export function parseDatabaseConnectionOptions(value?: string): Record<string, string> {
  const text = `${value || ""}`.trim();
  if (!text) {
    return {};
  }
  const parsed = JSON.parse(text) as unknown;
  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
    throw new Error("database connection options must be a JSON object");
  }
  return Object.fromEntries(
    Object.entries(parsed).map(([key, item]) => [key, item == null ? "" : String(item)]),
  );
}

export function databaseConnectionToForm(
  record: DatabaseConnectionItem,
): DatabaseConnectionFormValues {
  return {
    display_name: record.display_name,
    description: record.description,
    db_type: record.db_type,
    host: record.host,
    port: record.port,
    database_name: record.database_name,
    username: record.username,
    password: "",
    options: record.options || {},
    options_text: Object.keys(record.options || {}).length > 0
      ? JSON.stringify(record.options, null, 2)
      : "",
  };
}

interface DatabaseConnectionModalProps {
  open: boolean;
  editing?: DatabaseConnectionItem | null;
  saving?: boolean;
  onCancel: () => void;
  onSubmit: (payload: DatabaseConnectionPayload) => Promise<void>;
}

export default function DatabaseConnectionModal({
  open,
  editing,
  saving = false,
  onCancel,
  onSubmit,
}: DatabaseConnectionModalProps) {
  const { t } = useTranslation();
  const [form] = Form.useForm<DatabaseConnectionFormValues>();

  useEffect(() => {
    if (!open) {
      return;
    }
    if (editing) {
      form.setFieldsValue(databaseConnectionToForm(editing));
      return;
    }
    form.resetFields();
    form.setFieldsValue({ db_type: "postgresql", port: 5432 });
  }, [editing, form, open]);

  const handleOk = async () => {
    const values = await form.validateFields();
    const payload: DatabaseConnectionPayload = {
      display_name: values.display_name.trim(),
      description: values.description?.trim(),
      db_type: values.db_type,
      host: values.host.trim(),
      port: values.port,
      database_name: values.database_name.trim(),
      username: values.username.trim(),
      password: values.password,
      options: parseDatabaseConnectionOptions(values.options_text),
    };
    if (editing && !payload.password) {
      delete payload.password;
    }
    await onSubmit(payload);
  };

  return (
    <Modal
      title={editing ? t("admin.dataSourceDatabaseEditTitle") : t("admin.dataSourceDatabaseCreateTitle")}
      open={open}
      okText={t("common.save")}
      cancelText={t("common.cancel")}
      confirmLoading={saving}
      destroyOnHidden
      onOk={() => void handleOk()}
      onCancel={onCancel}
    >
      <Form<DatabaseConnectionFormValues> form={form} layout="vertical">
        <Form.Item
          name="display_name"
          label={t("admin.dataSourceDatabaseName")}
          rules={[{ required: true, message: t("admin.dataSourceDatabaseNameRequired") }]}
        >
          <Input />
        </Form.Item>
        <Form.Item name="description" label={t("admin.dataSourceDatabaseDescription")}>
          <Input />
        </Form.Item>
        <Space.Compact block>
          <Form.Item
            name="db_type"
            label={t("admin.dataSourceDatabaseType")}
            rules={[{ required: true }]}
            style={{ width: "40%" }}
          >
            <Select
              options={[
                { label: "PostgreSQL", value: "postgresql" },
                { label: "MySQL", value: "mysql" },
              ]}
              onChange={(value) => form.setFieldValue("port", value === "mysql" ? 3306 : 5432)}
            />
          </Form.Item>
          <Form.Item
            name="port"
            label={t("admin.dataSourceDatabasePort")}
            rules={[{ required: true }]}
            style={{ width: "60%" }}
          >
            <InputNumber min={1} max={65535} style={{ width: "100%" }} />
          </Form.Item>
        </Space.Compact>
        <Form.Item
          name="host"
          label={t("admin.dataSourceDatabaseHost")}
          rules={[{ required: true, message: t("admin.dataSourceDatabaseHostRequired") }]}
        >
          <Input placeholder="db.example.com" />
        </Form.Item>
        <Form.Item
          name="database_name"
          label={t("admin.dataSourceDatabaseDatabaseName")}
          rules={[{ required: true, message: t("admin.dataSourceDatabaseDatabaseNameRequired") }]}
        >
          <Input />
        </Form.Item>
        <Form.Item
          name="username"
          label={t("admin.dataSourceDatabaseUsername")}
          rules={[{ required: true, message: t("admin.dataSourceDatabaseUsernameRequired") }]}
        >
          <Input />
        </Form.Item>
        <Form.Item
          name="password"
          label={t("admin.dataSourceDatabasePassword")}
          rules={editing ? [] : [{ required: true, message: t("admin.dataSourceDatabasePasswordRequired") }]}
        >
          <Input.Password placeholder={editing ? t("admin.dataSourceDatabasePasswordEditPlaceholder") : undefined} />
        </Form.Item>
        <Form.Item
          name="options_text"
          label={t("admin.dataSourceDatabaseOptionsJson")}
          rules={[
            {
              validator: async (_, value) => {
                try {
                  parseDatabaseConnectionOptions(value);
                } catch {
                  return Promise.reject(new Error(t("admin.dataSourceDatabaseOptionsJsonInvalid")));
                }
                return Promise.resolve();
              },
            },
          ]}
        >
          <Input.TextArea rows={3} placeholder='{"sslmode":"require"}' />
        </Form.Item>
      </Form>
    </Modal>
  );
}
