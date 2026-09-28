import { useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { Alert, Button, Empty, Form, Input, Modal, Space, Switch, Table, Tooltip, message } from "antd";
import type { ColumnsType } from "antd/es/table";
import { DeleteOutlined, EditOutlined, PlusOutlined } from "@ant-design/icons";
import { isAxiosError } from "axios";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  createUserEnvironmentVariable,
  deleteUserEnvironmentVariable,
  listUserEnvironmentVariables,
  patchUserEnvironmentVariable,
  type UserEnvironmentVariable,
  type UserEnvironmentVariablePayload,
} from "./userEnvApi";

interface EnvFormValues {
  name: string;
  value?: string;
  description?: string;
  enabled: boolean;
}

interface Props {
  headingRef?: RefObject<HTMLHeadingElement>;
}

const envNamePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;
const controlEnvNamePattern = /^(LD_|DYLD_)/i;
const blockedEnvNames = new Set([
  "HOME",
  "PATH",
  "PYTHONPATH",
  "PYTHONHOME",
  "PYTHONSTARTUP",
  "PYTHONEXECUTABLE",
  "LD_LIBRARY_PATH",
  "LD_PRELOAD",
  "DYLD_LIBRARY_PATH",
  "DYLD_INSERT_LIBRARIES",
  "SHELL",
  "PWD",
  "IFS",
  "ENV",
  "BASH_ENV",
  "HTTP_PROXY",
  "HTTPS_PROXY",
  "ALL_PROXY",
  "NO_PROXY",
  "FTP_PROXY",
  "SSL_CERT_FILE",
  "SSL_CERT_DIR",
  "REQUESTS_CA_BUNDLE",
  "CURL_CA_BUNDLE",
  "SSLKEYLOGFILE",
  "NODE_OPTIONS", "NODE_EXTRA_CA_CERTS", "RUBYOPT", "RUBYLIB", "PERL5OPT", "PERL5LIB",
  "GIT_CONFIG", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_COUNT",
  "GIT_SSH_COMMAND", "ZDOTDIR", "PROMPT_COMMAND",
  "PYTHONINSPECT", "PYTHONBREAKPOINT", "NODE_PATH", "NODE_TLS_REJECT_UNAUTHORIZED",
  "OPENSSL_CONF", "OPENSSL_MODULES", "GIT_CONFIG_PARAMETERS",
  "GIT_SSL_NO_VERIFY", "GIT_SSL_CAINFO", "GIT_SSL_CAPATH",
]);

export function validateEnvName(t: TFunction, value?: string) {
  const name = (value || "").trim();
  if (!name) return Promise.reject(new Error(t("settingsPage.envVars.nameRequired")));
  if (name.length > 128 || !envNamePattern.test(name)) {
    return Promise.reject(new Error(t("settingsPage.envVars.nameInvalid")));
  }
  const normalized = name.toUpperCase();
  if (blockedEnvNames.has(normalized) || controlEnvNamePattern.test(normalized)) {
    return Promise.reject(new Error(t("settingsPage.envVars.nameReserved")));
  }
  return Promise.resolve();
}

export default function UserEnvironmentVariablesSettings({ headingRef }: Props) {
  const { t, i18n } = useTranslation();
  const [items, setItems] = useState<UserEnvironmentVariable[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const submitting = useRef(false);
  const [editing, setEditing] = useState<UserEnvironmentVariable | null>(null);
  const [deleting, setDeleting] = useState<UserEnvironmentVariable | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [editConflict, setEditConflict] = useState(false);
  const [form] = Form.useForm<EnvFormValues>();
  const pending = useRef(new Set<string>());
  const [pendingIds, setPendingIds] = useState(new Set<string>());

  useEffect(() => {
    let active = true;
    setLoading(true);
    const load = async () => {
      try {
        const result = await listUserEnvironmentVariables();
        if (active) setItems(result);
      } catch {
        // The shared request interceptor displays API errors.
      } finally {
        if (active) setLoading(false);
      }
    };
    void load();
    return () => { active = false; };
  }, []);

  const openCreate = () => {
    if (loading || submitting.current) return;
    setEditing(null);
    setEditConflict(false);
    form.resetFields();
    form.setFieldsValue({ enabled: true });
    setModalOpen(true);
  };

  const openEdit = (row: UserEnvironmentVariable) => {
    if (submitting.current) return;
    setEditing(row);
    setEditConflict(false);
    form.resetFields();
    form.setFieldsValue({
      name: row.name,
      description: row.description,
      enabled: row.enabled,
      value: "",
    });
    setModalOpen(true);
  };

  const onSubmit = async () => {
    if (submitting.current || editConflict) return;
    submitting.current = true;
    setSaving(true);
    let values: EnvFormValues;
    try {
      values = await form.validateFields();
    } catch {
      submitting.current = false;
      setSaving(false);
      return;
    }
    try {
      if (editing) {
        const payload: UserEnvironmentVariablePayload = {};
        const name = values.name.trim();
        const description = (values.description || "").trim();
        if (name !== editing.name) payload.name = name;
        if (values.enabled !== editing.enabled) payload.enabled = values.enabled;
        if (description !== editing.description) payload.description = description;
        if (values.value) payload.value = values.value;
        if (Object.keys(payload).length === 0) {
          setModalOpen(false);
          return;
        }
        payload.expected_updated_at = editing.updated_at;
        const updated = await patchUserEnvironmentVariable(editing.id, payload);
        setItems((current) => current.map((item) => (item.id === updated.id ? updated : item)));
      } else {
        const created = await createUserEnvironmentVariable({
          name: values.name.trim(),
          value: values.value || "",
          enabled: values.enabled,
          description: values.description || "",
        });
        setItems((current) => [created, ...current]);
      }
      setModalOpen(false);
      message.success(t("settingsPage.envVars.saved"));
    } catch (error) {
      // Keep the dialog open; the request interceptor displays the error.
      if (editing && isAxiosError(error) && error.response?.status === 409) {
        setEditConflict(true);
        await reloadItem(editing.id);
      }
    } finally {
      submitting.current = false;
      setSaving(false);
    }
  };

  const reloadItem = async (id: string) => {
    try {
      const latest = (await listUserEnvironmentVariables()).find((item) => item.id === id);
      // Other rows may have been saved or deleted while this request was in flight.
      setItems((current) => latest
        ? current.map((item) => item.id === id ? latest : item)
        : current.filter((item) => item.id !== id));
    } catch {
      // The shared request interceptor displays API errors.
    }
  };

  const toggleEnabled = async (row: UserEnvironmentVariable, enabled: boolean) => {
    if (pending.current.has(row.id)) return;
    pending.current.add(row.id);
    setPendingIds(new Set(pending.current));
    try {
      const updated = await patchUserEnvironmentVariable(row.id, { enabled, expected_updated_at: row.updated_at });
      setItems((current) => current.map((item) => (item.id === updated.id ? updated : item)));
    } catch (error) {
      // Preserve the confirmed state on failure.
      if (isAxiosError(error) && error.response?.status === 409) await reloadItem(row.id);
    } finally {
      pending.current.delete(row.id);
      setPendingIds(new Set(pending.current));
    }
  };

  const remove = async (row: UserEnvironmentVariable) => {
    if (pending.current.has(row.id)) return;
    pending.current.add(row.id);
    setPendingIds(new Set(pending.current));
    try {
      await deleteUserEnvironmentVariable(row.id);
      setItems((current) => current.filter((item) => item.id !== row.id));
      setDeleting(null);
      message.success(t("settingsPage.envVars.deleted"));
    } catch {
      // The shared request interceptor displays API errors.
    } finally {
      pending.current.delete(row.id);
      setPendingIds(new Set(pending.current));
    }
  };

  const columns: ColumnsType<UserEnvironmentVariable> = [
    {
      title: t("settingsPage.envVars.name"),
      dataIndex: "name",
      key: "name",
      width: 240,
      render: (name: string) => <code className="settings-env-var-name">{name}</code>,
    },
    {
      title: t("settingsPage.envVars.secret"),
      dataIndex: "masked_value",
      key: "masked_value",
      width: 180,
      render: (value: string, row) => (
        <Button type="link" className="settings-env-secret-button" disabled={saving || pendingIds.has(row.id)} onClick={() => openEdit(row)}>
          {row.credential_status === "invalid_name" ? t("settingsPage.envVars.invalidNameTitle")
            : row.credential_status === "unavailable" ? t("settingsPage.envVars.secretUnavailable") : value || "••••••••"}
        </Button>
      ),
    },
    {
      title: t("settingsPage.envVars.usage"),
      dataIndex: "enabled",
      key: "enabled",
      width: 92,
      render: (enabled: boolean, row) => (
        <Switch aria-label={t("settingsPage.envVars.enableAria", { name: row.name })} checked={enabled} loading={pendingIds.has(row.id)} disabled={saving} onChange={(checked: boolean) => void toggleEnabled(row, checked)} />
      ),
    },
    {
      title: t("settingsPage.envVars.notes"),
      dataIndex: "description",
      key: "description",
      ellipsis: true,
      render: (description: string) => description || <span className="settings-env-muted">{t("settingsPage.envVars.noNotes")}</span>,
    },
    {
      title: t("settingsPage.envVars.updatedAt"),
      dataIndex: "updated_at",
      key: "updated_at",
      width: 170,
      render: (value: string) => new Date(value).toLocaleString(i18n.language),
    },
    {
      title: "",
      key: "actions",
      width: 96,
      align: "right",
      render: (_, row) => (
        <Space size={4}>
          <Tooltip title={t("common.edit")}>
            <Button aria-label={t("settingsPage.envVars.editAria", { name: row.name })} disabled={saving || pendingIds.has(row.id)} icon={<EditOutlined />} type="text" onClick={() => openEdit(row)} />
          </Tooltip>
          <Button title={t("settingsPage.envVars.deleteTitle")} aria-label={t("settingsPage.envVars.deleteAria", { name: row.name })} disabled={saving || pendingIds.has(row.id)} danger icon={<DeleteOutlined />} type="text" onClick={() => setDeleting(row)} />
        </Space>
      ),
    },
  ];

  return (
    <>
      <header className="settings-detail-header">
        <div>
          <h1 ref={headingRef} tabIndex={-1}>{t("settingsPage.sections.envVars")}</h1>
          <p>{t("settingsPage.envVars.description")}</p>
        </div>
        <Button icon={<PlusOutlined />} type="primary" disabled={loading || saving} onClick={openCreate}>{t("settingsPage.envVars.add")}</Button>
      </header>
      <section className="settings-env-panel">
        <Table
          columns={columns}
          dataSource={items}
          loading={loading}
          locale={{ emptyText: <Empty description={t("settingsPage.envVars.empty")} /> }}
          pagination={false}
          rowKey="id"
          scroll={{ x: 860 }}
        />
      </section>
      <Modal
        centered
        title={t("settingsPage.envVars.deleteTitle")}
        open={deleting !== null}
        okText={t("common.delete")}
        okButtonProps={{ danger: true }}
        cancelText={t("common.cancel")}
        confirmLoading={!!deleting && pendingIds.has(deleting.id)}
        onOk={() => { if (deleting) void remove(deleting); }}
        onCancel={() => { if (deleting && !pending.current.has(deleting.id)) setDeleting(null); }}
        cancelButtonProps={{ disabled: !!deleting && pendingIds.has(deleting.id) }}
        closable={!deleting || !pendingIds.has(deleting.id)}
        maskClosable={false}
        destroyOnHidden
      >
        <p>{t("settingsPage.envVars.deleteConfirm", { name: deleting?.name })}</p>
      </Modal>
      <Modal
        title={t(editing ? "settingsPage.envVars.editTitle" : "settingsPage.envVars.createTitle")}
        open={modalOpen}
        okText={t("common.save")}
        cancelText={t("common.cancel")}
        confirmLoading={saving}
        okButtonProps={{ disabled: editConflict }}
        onCancel={() => { if (!submitting.current) setModalOpen(false); }}
        closable={!saving}
        maskClosable={!saving}
        keyboard={!saving}
        cancelButtonProps={{ disabled: saving }}
        onOk={() => void onSubmit()}
        destroyOnHidden
      >
        {editConflict && <Alert type="warning" showIcon message={t("settingsPage.envVars.conflict")} />}
        {editing?.credential_status === "invalid_name" && (
          <Alert type="warning" showIcon message={t("settingsPage.envVars.invalidNameTitle")} description={t("settingsPage.envVars.invalidNameDescription")} />
        )}
        {editing?.credential_status === "unavailable" && (
          <Alert type="warning" showIcon message={t("settingsPage.envVars.unavailableTitle")} description={t("settingsPage.envVars.unavailableDescription")} />
        )}
        <Form form={form} layout="vertical" initialValues={{ enabled: true }} disabled={saving}>
          <Form.Item
            label={t("settingsPage.envVars.name")}
            name="name"
            rules={[{ validator: (_, value) => validateEnvName(t, value) }]}
            extra={t("settingsPage.envVars.nameHelp")}
          >
            <Input maxLength={128} autoComplete="off" placeholder="TAVILY_API_KEY" />
          </Form.Item>
          <Form.Item
            label={t(editing ? "settingsPage.envVars.newSecret" : "settingsPage.envVars.secret")}
            name="value"
            rules={[
              { required: !editing, message: t("settingsPage.envVars.secretRequired") },
              { validator: (_, value: string | undefined) => value && !value.trim() ? Promise.reject(new Error(t("settingsPage.envVars.secretWhitespace"))) : Promise.resolve() },
              { validator: (_, value: string | undefined) => value?.includes("\0") ? Promise.reject(new Error(t("settingsPage.envVars.secretNull"))) : Promise.resolve() },
            ]}
            extra={editing ? t("settingsPage.envVars.secretUnchanged") : undefined}
          >
            <Input.Password autoComplete="new-password" placeholder={t(editing ? "settingsPage.envVars.keepUnchanged" : "settingsPage.envVars.secretPlaceholder")} />
          </Form.Item>
          <Form.Item label={t("settingsPage.envVars.notes")} name="description">
            <Input.TextArea maxLength={512} autoSize={{ minRows: 2, maxRows: 4 }} placeholder={t("settingsPage.envVars.notesPlaceholder")} />
          </Form.Item>
          <Form.Item label={t("settingsPage.envVars.enabled")} name="enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
