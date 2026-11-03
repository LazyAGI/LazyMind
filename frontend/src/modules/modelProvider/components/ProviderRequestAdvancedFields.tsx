import { Form, Input, type FormInstance } from "antd";
import { useTranslation } from "react-i18next";
import type { ProviderRequestAdvanced } from "../api";

export type AdvancedRequestFormValues = { headers: string; query: string; extra_body: string };

function objectValue(raw: string | undefined): Record<string, unknown> {
  if (!raw?.trim()) return {};
  const parsed: unknown = JSON.parse(raw);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("jsonObject");
  return parsed as Record<string, unknown>;
}

export function advancedRequestInput(values?: AdvancedRequestFormValues): ProviderRequestAdvanced {
  const result: ProviderRequestAdvanced = {};
  for (const field of ["headers", "query", "extra_body"] as const) {
    const parsed = objectValue(values?.[field]);
    if (Object.keys(parsed).length) {
      if (field === "extra_body") result.extra_body = parsed;
      else {
        if (Object.values(parsed).some((value) => typeof value !== "string")) throw new Error("stringValues");
        result[field] = parsed as Record<string, string>;
      }
    }
  }
  if (new TextEncoder().encode(JSON.stringify(result)).length > 6144) throw new Error("advancedMax");
  return result;
}

export function advancedRequestFormValues(value?: ProviderRequestAdvanced): AdvancedRequestFormValues {
  const json = (field: keyof ProviderRequestAdvanced) => Object.keys(value?.[field] || {}).length
    ? JSON.stringify(value?.[field], null, 2) : "";
  return {headers: json("headers"), query: json("query"), extra_body: json("extra_body")};
}

export function ProviderRequestAdvancedFields({form}: {form: FormInstance}) {
  const {t} = useTranslation();
  const text = (key: string) => t(`modelProvider.requestConfig.${key}`);
  return <details className="provider-request-advanced-fields">
    <summary>{text("advanced")}</summary>
    <p className="provider-request-config-hint">{text("advancedHint")}</p>
    {([['headers', 'headers'], ['query', 'query'], ['extra_body', 'extraBody']] as const).map(([field, label]) =>
      <Form.Item key={field} name={["advanced", field]} label={text(label)} rules={[{validator: async (_rule, raw: string) => {
        try {
          const parsed = objectValue(raw);
          if (field !== "extra_body" && Object.values(parsed).some((value) => typeof value !== "string")) {
            throw new Error("stringValues");
          }
          advancedRequestInput({...form.getFieldValue("advanced"), [field]: raw});
        } catch (error) {
          const key = error instanceof SyntaxError ? "jsonObject" : error instanceof Error ? error.message : "jsonObject";
          throw new Error(text(key));
        }
      }}]} extra={field === "extra_body" ? text("extraBodyHint") : undefined}>
        <Input.TextArea aria-label={text(label)} autoComplete="off" spellCheck={false} autoSize={{minRows: 3, maxRows: 8}} placeholder="{}" />
      </Form.Item>,
    )}
  </details>;
}
