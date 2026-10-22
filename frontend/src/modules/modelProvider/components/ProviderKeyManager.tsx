import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  KeyOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import {
  Button,
  Drawer,
  Form,
  Input,
  Modal,
  Popconfirm,
  Switch,
  Tooltip,
  message,
} from "antd";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { useSettingsFormDraft } from "@/modules/settings/SettingsNavigationGuard";
import { replaceProviderKey, revealProviderKey } from "../api";
import "./ProviderKeyManager.scss";

export interface ProviderKeyManagerProps {
  providerId: string;
  providerName: string;
  groupId: string;
  keys: Array<{ id: string; masked: string; enabled?: boolean }>;
  disabled?: boolean;
  changingKey?: string;
  removingKey?: string;
  onAdd: () => void;
  onDelete: (id: string) => Promise<void>;
  onToggle: (id: string, enabled: boolean) => Promise<void>;
  onReload: () => Promise<void>;
  checkAction?: ReactNode;
}

export function ProviderKeyManager(props: ProviderKeyManagerProps) {
  const { t } = useTranslation();
  const text = (key: string, number?: number) =>
    t(`modelProvider.keyManager.${key}`, { number });
  const workspace = (key: string, number?: number) =>
    t(`modelProvider.workspace.${key}`, { number });
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState("");
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<{ id: string; number: number }>();
  const [secrets, setSecrets] = useState<{
    context: string;
    values: Record<string, string>;
  }>({ context: "", values: {} });
  const [form] = Form.useForm<{ apiKey: string }>();
  const draft = useSettingsFormDraft(form, Boolean(editing), saving);
  const context = `${props.providerId}\0${props.groupId}\0${props.keys.map((key) => key.id).join("\0")}`;
  const currentContext = useRef(context);
  currentContext.current = context;
  const visibleSecrets = secrets.context === context ? secrets.values : {};
  const busy = Boolean(
    props.disabled ||
      props.changingKey ||
      props.removingKey ||
      pending ||
      saving,
  );
  useEffect(() => {
    setSecrets({ context, values: {} });
    setPending("");
    setEditing(undefined);
    form.resetFields();
  }, [context, form]);
  useEffect(() => {
    setOpen(false);
  }, [props.providerId, props.groupId]);
  useEffect(() => {
    currentContext.current = context;
    return () => {
      currentContext.current = "";
    };
  }, [context]);

  const close = () => {
    setOpen(false);
    setSecrets({ context, values: {} });
  };
  const reveal = async (keyId: string, copy = false) => {
    if (busy) return;
    if (!copy && visibleSecrets[keyId]) {
      setSecrets({
        context,
        values: Object.fromEntries(
          Object.entries(visibleSecrets).filter(([id]) => id !== keyId),
        ),
      });
      return;
    }
    setPending(keyId);
    try {
      const value = await revealProviderKey(
        props.providerId,
        props.groupId,
        keyId,
      );
      if (currentContext.current !== context) return;
      if (copy) {
        await navigator.clipboard.writeText(value);
        message.success(text("copied"));
      } else
        setSecrets({ context, values: { ...visibleSecrets, [keyId]: value } });
    } catch (error) {
      if (currentContext.current === context)
        message.error(getLocalizedErrorMessage(error));
    } finally {
      if (currentContext.current === context) setPending("");
    }
  };
  const edit = (id: string, number: number) => {
    form.resetFields();
    setEditing({ id, number });
    draft.captureSavedValues();
  };
  const save = async ({ apiKey }: { apiKey: string }) => {
    if (!editing || saving) return;
    setSaving(true);
    try {
      await replaceProviderKey(
        props.providerId,
        props.groupId,
        editing.id,
        apiKey.trim(),
      );
      if (currentContext.current !== context) return;
      draft.acceptSaved();
      setEditing(undefined);
      form.resetFields();
      setSecrets({ context, values: {} });
      await props.onReload();
      message.success(text("updated"));
    } catch (error) {
      if (currentContext.current === context)
        message.error(getLocalizedErrorMessage(error));
    } finally {
      setSaving(false);
    }
  };
  const rows = (inDrawer = false) =>
    props.keys.map((key, index) => (
      <div
        className={`provider-workspace-key provider-key-row${inDrawer ? " in-drawer" : ""}`}
        key={key.id}
      >
        <span>{workspace("keyNumber", index + 1)}</span>
        <code>{visibleSecrets[key.id] || key.masked}</code>
        <span
          className={`provider-workspace-key-state${key.enabled ? " is-enabled" : ""}`}
        >
          {workspace(key.enabled ? "keyEnabled" : "keyDisabled")}
        </span>
        <Switch
          size="small"
          aria-label={workspace("useKeyNumber", index + 1)}
          checked={key.enabled === true}
          disabled={busy}
          loading={props.changingKey === key.id}
          onChange={(enabled) => void props.onToggle(key.id, enabled)}
        />
        <div className="provider-key-row-actions">
          <Tooltip title={text("copy", index + 1)}>
            <Button
              type="text"
              aria-label={text("copy", index + 1)}
              icon={<CopyOutlined aria-hidden="true" />}
              disabled={busy}
              onClick={() => void reveal(key.id, true)}
            />
          </Tooltip>
          <Tooltip title={text("edit", index + 1)}>
            <Button
              type="text"
              aria-label={text("edit", index + 1)}
              icon={<EditOutlined aria-hidden="true" />}
              disabled={busy}
              onClick={() => edit(key.id, index + 1)}
            />
          </Tooltip>
          <Popconfirm
            title={workspace("deleteKey")}
            description={workspace("deleteKeyHint")}
            okText={t("common.delete")}
            cancelText={t("common.cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={() => props.onDelete(key.id)}
          >
            <Button
              type="text"
              aria-label={workspace("deleteKeyNumber", index + 1)}
              icon={<DeleteOutlined aria-hidden="true" />}
              disabled={busy}
              loading={props.removingKey === key.id}
            />
          </Popconfirm>
          <Tooltip
            title={text(visibleSecrets[key.id] ? "hide" : "reveal", index + 1)}
          >
            <Button
              type="text"
              aria-label={text(
                visibleSecrets[key.id] ? "hide" : "reveal",
                index + 1,
              )}
              icon={
                visibleSecrets[key.id] ? (
                  <EyeInvisibleOutlined aria-hidden="true" />
                ) : (
                  <EyeOutlined aria-hidden="true" />
                )
              }
              disabled={busy}
              loading={pending === key.id}
              onClick={() => void reveal(key.id)}
            />
          </Tooltip>
        </div>
      </div>
    ));
  return (
    <>
      <div className="provider-workspace-key-heading">
        <h3>
          API Key{" "}
          <span className="provider-workspace-count">{props.keys.length}</span>
        </h3>
        <div className="provider-workspace-key-actions">
          {props.checkAction}
          <Button
            type="link"
            icon={<PlusOutlined aria-hidden="true" />}
            disabled={busy}
            onClick={props.onAdd}
          >
            {workspace("addKey")}
          </Button>
          <Tooltip title={text("manage")}>
            <Button
              type="text"
              aria-label={text("manage")}
              icon={<KeyOutlined aria-hidden="true" />}
              onClick={() => setOpen(true)}
            />
          </Tooltip>
        </div>
      </div>
      <div className="provider-workspace-keys">
        {props.keys.length ? (
          rows()
        ) : (
          <p className="provider-workspace-key-empty">{workspace("noKeys")}</p>
        )}
      </div>
      <Drawer
        title={`${props.providerName} · ${text("manage")}`}
        open={open}
        width={580}
        onClose={close}
        className="provider-key-manager-drawer"
        destroyOnHidden
        footer={
          <Button
            icon={<PlusOutlined aria-hidden="true" />}
            onClick={() => {
              close();
              props.onAdd();
            }}
            disabled={busy}
          >
            {workspace("addKey")}
          </Button>
        }
      >
        <p className="provider-key-manager-hint">{text("singleActiveHint")}</p>
        {props.keys.length ? rows(true) : <p>{workspace("noKeys")}</p>}
      </Drawer>
      <Modal
        title={text("edit", editing?.number)}
        open={Boolean(editing)}
        centered
        confirmLoading={saving}
        okText={t("common.save")}
        cancelText={t("common.cancel")}
        onOk={() => form.submit()}
        onCancel={() =>
          draft.confirmClose(() => {
            setEditing(undefined);
            form.resetFields();
          })
        }
        destroyOnHidden
        maskClosable={!saving}
      >
        <Form
          form={form}
          name="replace-provider-key"
          layout="vertical"
          onFinish={save}
        >
          <Form.Item
            label="API Key"
            name="apiKey"
            extra={text("replaceHint")}
            rules={[
              {
                required: true,
                whitespace: true,
                message: t("modelProvider.validation.apiKeyRequired"),
              },
              { max: 512, message: t("modelProvider.validation.apiKeyMax") },
              {
                pattern: /^\S+$/,
                message: t("modelProvider.validation.apiKeyNoSpaces"),
              },
            ]}
          >
            <Input.Password autoComplete="new-password" maxLength={512} />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
