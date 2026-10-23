import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import {
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
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
import { useFocusModality } from "../hooks/useFocusModality";
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
  onCreate?: (apiKey: string) => Promise<boolean | void>;
  onDelete: (id: string) => Promise<void>;
  onToggle: (id: string, enabled: boolean) => Promise<void>;
  onReload: () => Promise<void>;
  checkAction?: ReactNode;
}

export function ProviderKeyManager(props: ProviderKeyManagerProps) {
  const { t } = useTranslation();
  const focusModality = useFocusModality();
  const text = (key: string, number?: number) =>
    t(`modelProvider.keyManager.${key}`, { number });
  const workspace = (key: string, number?: number) =>
    t(`modelProvider.workspace.${key}`, { number });
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState("");
  const [saving, setSaving] = useState(false);
  const [inlineValue, setInlineValue] = useState("");
  const [inlineError, setInlineError] = useState("");
  const [inlineSaving, setInlineSaving] = useState(false);
  const inlineErrorId = useId();
  const inlineRequest = useRef<{ context: string; promise: Promise<boolean> } | null>(null);
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
      saving ||
      inlineSaving,
  );
  useEffect(() => {
    setSecrets({ context, values: {} });
    setPending("");
    setEditing(undefined);
    setInlineValue("");
    setInlineError("");
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
  const saveInline = (): Promise<boolean> => {
    if (inlineRequest.current)
      return inlineRequest.current.context === context ? inlineRequest.current.promise : Promise.resolve(false);
    const value = inlineValue.trim();
    if (!value) return Promise.resolve(true);
    if (busy || !props.onCreate) return Promise.resolve(false);
    const invalid = value.length > 512 ? "apiKeyMax" : /\s/.test(value) ? "apiKeyNoSpaces" : "";
    if (invalid) {
      setInlineError(t(`modelProvider.validation.${invalid}`));
      return Promise.resolve(false);
    }
    setInlineError("");
    setInlineSaving(true);
    const request: { context: string; promise: Promise<boolean> } = {
      context,
      promise: Promise.resolve().then(async () => {
        try {
          const saved = await props.onCreate!(value);
          if (currentContext.current === context && saved !== false) setInlineValue("");
          return saved !== false;
        } catch (error) {
          if (currentContext.current === context) setInlineError(getLocalizedErrorMessage(error));
          return false;
        } finally {
          if (inlineRequest.current === request) inlineRequest.current = null;
          setInlineSaving(false);
        }
      }),
    };
    inlineRequest.current = request;
    return request.promise;
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
        <code>{visibleSecrets[key.id] || `****${key.masked.replace(/^\*+(?:\.\.\.)?/, "").slice(-4)}`}</code>
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
              className="provider-key-row-reveal"
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
        <div className="provider-key-row-usage">
          <Switch
            size="small"
            aria-label={workspace("useKeyNumber", index + 1)}
            checked={key.enabled === true}
            disabled={busy}
            loading={props.changingKey === key.id}
            onChange={(enabled) => void props.onToggle(key.id, enabled)}
          />
          <span
            className={`provider-workspace-key-state${key.enabled ? " is-enabled" : ""}`}
          >
            {workspace(key.enabled ? "keyEnabled" : "keyDisabled")}
          </span>
        </div>
      </div>
    ));
  return (
    <>
      <div className="provider-workspace-key-heading">
        <h3>
          {t("modelProvider.apiKeyLabel")}
        </h3>
        <div className="provider-workspace-key-actions" onClickCapture={(event) => {
          if (inlineRequest.current) {
            event.preventDefault();
            event.stopPropagation();
          }
        }}>
          {props.checkAction}
        </div>
      </div>
      <div className="provider-key-field-row">
        <div className="provider-workspace-keys">
          {props.keys.length ? (
            rows()
          ) : (
            props.onCreate ? <div className="provider-key-inline-create">
              <Input
                type="password"
                aria-label={t("modelProvider.apiKeyLabel")}
                aria-invalid={Boolean(inlineError)}
                aria-describedby={inlineError ? inlineErrorId : undefined}
                autoComplete="new-password"
                placeholder={workspace("noKeys")}
                value={inlineValue}
                status={inlineError ? "error" : undefined}
                disabled={busy}
                maxLength={512}
                onChange={(event) => { setInlineValue(event.target.value); setInlineError(""); }}
                onPressEnter={(event) => {
                  if (event.nativeEvent.isComposing) return;
                  event.preventDefault();
                  void saveInline();
                }}
                onBlur={() => void saveInline()}
              />
              {inlineError ? <p id={inlineErrorId} role="alert">{inlineError}</p> : null}
            </div> : <p className="provider-workspace-key-empty">{workspace("noKeys")}</p>
          )}
        </div>
        <Tooltip title={text("manage")}>
          <Button
            type="text"
            className="provider-workspace-icon-button"
            aria-label={text("manage")}
            icon={
              <svg
                width="18"
                height="18"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
                focusable="false"
              >
                <circle cx="7" cy="12" r="4.5" />
                <path d="M11.5 12H22m-5 0v5m4-5v5" />
              </svg>
            }
            onClick={() => {
              const supplier = props.providerId;
              void saveInline().then((saved) => {
                if (saved && currentContext.current.split("\0")[0] === supplier) setOpen(true);
              });
            }}
          />
        </Tooltip>
      </div>
      <Drawer
        rootClassName={`model-provider-focus-${focusModality}`}
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
        rootClassName={`model-provider-focus-${focusModality}`}
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
            label={t("modelProvider.apiKeyLabel")}
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
