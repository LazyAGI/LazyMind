import { Form, Input, InputNumber } from "antd";
import { useTranslation } from "react-i18next";

/** Shares the model editor Form so dirty-state and validation use one draft. */
export function EmbeddingOptionsEditor({ disabled = false }: { disabled?: boolean }) {
  const { t } = useTranslation();
  const text = (key: string) => t(`modelProvider.editor.${key}`);
  const prefixRules = [{ validator: (_: unknown, value?: string) => !value || Array.from(value).length <= 2048
    ? Promise.resolve() : Promise.reject(new Error(text("embeddingPrefixInvalid"))) }];
  return <>
    <Form.Item name="embeddingQueryPrefix" label={text("embeddingQueryPrefix")} extra={text("embeddingPrefixHint")}
      rules={prefixRules}>
      <Input.TextArea aria-label={text("embeddingQueryPrefix")} maxLength={4096} rows={2} autoComplete="off" disabled={disabled} />
    </Form.Item>
    <Form.Item name="embeddingPassagePrefix" label={text("embeddingPassagePrefix")} extra={text("embeddingPrefixHint")}
      rules={prefixRules}>
      <Input.TextArea aria-label={text("embeddingPassagePrefix")} maxLength={4096} rows={2} autoComplete="off" disabled={disabled} />
    </Form.Item>
    <Form.Item name="embeddingBatchSize" label={text("embeddingBatchSize")} extra={text("embeddingBatchSizeHint")}
      rules={[{ validator: (_, value?: number | null) => value == null || (Number.isInteger(value) && value >= 1 && value <= 2048)
        ? Promise.resolve() : Promise.reject(new Error(text("embeddingBatchSizeInvalid"))) }]}>
      <InputNumber aria-label={text("embeddingBatchSize")} min={1} max={2048} autoComplete="off" disabled={disabled} style={{ width: "100%" }} />
    </Form.Item>
  </>;
}
