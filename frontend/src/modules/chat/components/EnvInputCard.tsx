import { useLayoutEffect, useRef, useState } from "react";
import { Button, Form, Input, Space, Typography } from "antd";
import { LockOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { axiosInstance, BASE_URL } from "@/components/request";

export interface EnvironmentInput {
  name: string;
  scope: "user" | "conversation";
}

export interface EnvironmentInputResult extends EnvironmentInput {
  status: "configured" | "canceled";
  enabled?: boolean;
}

interface Props {
  conversationId: string;
  historyId?: string;
  askId: string;
  input: EnvironmentInput;
  result?: EnvironmentInputResult;
  disabled: boolean;
  onComplete: (result: EnvironmentInputResult) => Promise<void>;
}

export default function EnvInputCard(props: Props) {
  return <ScopedEnvInputCard key={JSON.stringify([props.conversationId, props.historyId, props.askId])} {...props} />;
}

function ScopedEnvInputCard({ conversationId, historyId, askId, input, result, disabled, onComplete }: Props) {
  const { t } = useTranslation();
  const [form] = Form.useForm<{ value: string }>();
  const [saving, setSaving] = useState(false);
  const pending = useRef(false);
  const [completed, setCompleted] = useState(result);
  const outcome = result || completed;
  const live = useRef<Pick<Props, "disabled" | "onComplete"> | null>(null);

  useLayoutEffect(() => {
    // Use the latest message callback, but never resume an unmounted conversation/card.
    live.current = { disabled, onComplete };
    return () => { live.current = null; };
  });

  const submit = async (cancel: boolean) => {
    if (pending.current || disabled || !historyId || outcome) return;
    pending.current = true;
    setSaving(true);
    try {
      const value = cancel ? undefined : (await form.validateFields()).value;
      if (!live.current || live.current.disabled) return;
      const response = await axiosInstance.post<{ data: EnvironmentInputResult }>(
        `${BASE_URL}/api/core/conversations/${encodeURIComponent(conversationId)}:env-input`,
        { history_id: historyId, ask_id: askId, cancel, ...(value !== undefined ? { value } : {}) },
      );
      if (!live.current) return;
      // Clear before resuming chat. Neither message state nor autosave receives the value.
      form.resetFields();
      const receipt = response.data.data;
      setCompleted(receipt);
      if (!live.current.disabled) await live.current.onComplete(receipt);
    } catch {
      // Field validation and the shared HTTP interceptor display safe errors.
    } finally {
      if (live.current) {
        pending.current = false;
        setSaving(false);
      }
    }
  };

  return (
    <section aria-label={t("settingsPage.envVars.secureInputTitle")} style={{
      width: "100%", maxWidth: 720, minWidth: 0, boxSizing: "border-box",
      padding: 16, border: "1px solid #e2e8f5", borderRadius: 8, background: "#fff",
    }}>
      <Space direction="vertical" size={12} style={{ width: "100%" }}>
        <Typography.Text strong><LockOutlined /> {t("settingsPage.envVars.secureInputTitle")}</Typography.Text>
        <Typography.Text style={{ overflowWrap: "anywhere" }}>
          {input.name} · {t(`settingsPage.envVars.scope.${input.scope}`)}
        </Typography.Text>
        {outcome ? (
          <Space>
            <Typography.Text>{t(`settingsPage.envVars.inputStatus.${
              outcome.status === "configured" && outcome.enabled === false ? "disabled" : outcome.status
            }`)}</Typography.Text>
            {!disabled && <Button disabled={saving} onClick={async () => {
              if (pending.current || !live.current || live.current.disabled) return;
              pending.current = true;
              setSaving(true);
              try { await live.current.onComplete(outcome); } catch {
                // The chat request displays its own error; keep the receipt available for retry.
              } finally {
                if (live.current) { pending.current = false; setSaving(false); }
              }
            }}>{t("settingsPage.envVars.resume")}</Button>}
          </Space>
        ) : disabled ? (
          <Typography.Text type="secondary">{t("settingsPage.envVars.inputInactive")}</Typography.Text>
        ) : (
          <Form form={form} layout="vertical" onFinish={() => void submit(false)}>
            <Form.Item name="value" label={t("settingsPage.envVars.inputValue")} rules={[
              { required: true, whitespace: true, message: t("settingsPage.envVars.secretRequired") },
              { validator: (_, value?: string) => value?.includes("\0") || value?.trim() === "<redacted>"
                ? Promise.reject(new Error(t("settingsPage.envVars.inputInvalid"))) : Promise.resolve() },
            ]}>
              <Input.Password autoComplete="new-password" disabled={disabled || saving} />
            </Form.Item>
            <Space>
              <Button disabled={disabled || saving || !historyId} onClick={() => void submit(true)}>{t("common.cancel")}</Button>
              <Button type="primary" loading={saving} disabled={disabled || !historyId} onClick={() => void submit(false)}>
                {t("common.save")}
              </Button>
            </Space>
          </Form>
        )}
      </Space>
    </section>
  );
}
