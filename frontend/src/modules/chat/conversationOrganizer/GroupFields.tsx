import { Form, Input } from "antd";
import { useTranslation } from "react-i18next";
import type { ReactNode } from "react";

export type GroupValues = { name: string; scope?: string };
export const normalizeGroupValues = (values: GroupValues) => ({
  name: values.name.trim(),
  scope: values.scope?.trim() || "",
});

export default function GroupFields({ scopeHint, nameDisabled = false }: { scopeHint?: ReactNode; nameDisabled?: boolean }) {
  const { t } = useTranslation();
  const count = (limit: number) => ({ formatter: ({ value }: { value: string }) => `${Array.from(value).length}/${limit}` });
  return <>
    <Form.Item name="name" label={t("conversationOrganizer.groupName")} rules={[{
      validator: async (_, value) => {
        const length = Array.from(String(value || "").trim()).length;
        if (!length || length > 24) throw new Error(t(length ? "conversationOrganizer.nameTooLong" : "conversationOrganizer.nameRequired"));
      },
    }]}><Input disabled={nameDisabled} showCount={count(24)} /></Form.Item>
    <Form.Item name="scope" label={t("conversationOrganizer.scope")} extra={scopeHint} rules={[{
      validator: async (_, value) => {
        if (Array.from(String(value || "")).length > 500) throw new Error(t("conversationOrganizer.scopeTooLong"));
      },
    }]}><Input.TextArea rows={3} showCount={count(500)} /></Form.Item>
  </>;
}
