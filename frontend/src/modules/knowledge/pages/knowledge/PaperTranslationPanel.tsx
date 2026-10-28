import { useState } from "react";
import { Button, Empty, Input, Modal, Select, Spin } from "antd";
import { SendOutlined, SettingOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { DocumentTranslationProvider } from "@/modules/knowledge/api/translation";
import { translationProviderDisplayName } from "@/modules/modelProvider/translationProviderNames";

interface Props {
  source: string;
  result: string;
  loading: boolean;
  providers: DocumentTranslationProvider[];
  providerGroupId?: string;
  providerSaving: boolean;
  providersLoading: boolean;
  onProviderChange: (id: string) => void;
  onTranslate: (text: string) => void;
}

export default function PaperTranslationPanel(props: Props) {
  const { i18n } = useTranslation();
  const language = i18n.resolvedLanguage || i18n.language || "zh-CN";
  const [draft, setDraft] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const submit = () => {
    const text = draft.trim();
    if (!text || props.providerSaving) return;
    setDraft("");
    props.onTranslate(text);
  };
  return <section className="knowledge-reader-translation" aria-label="论文翻译">
    <div className="knowledge-reader-translation-toolbar">
      <Button aria-label="设置翻译引擎" icon={<SettingOutlined />} size="small" onClick={() => setSettingsOpen(true)}>设置翻译引擎</Button>
    </div>
    <section className="knowledge-reader-translation-pane" aria-label="翻译原文">
      <h3>原文</h3>
      {props.source ? <div className="knowledge-translation-text">{props.source}</div>
        : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="选中论文文字后点击翻译，或在底部输入文字" />}
    </section>
    <section className="knowledge-reader-translation-pane is-result" aria-label="翻译结果" aria-live="polite" aria-busy={props.loading}>
      <h3>译文</h3>
      {props.loading ? <Spin tip="正在翻译…"><div className="knowledge-reader-translation-loading" /></Spin>
        : <div className="knowledge-translation-text">{props.result}</div>}
    </section>
    <form className="knowledge-reader-translation-composer" onSubmit={(event) => { event.preventDefault(); submit(); }}>
      <Input.TextArea aria-label="输入要翻译的文字" placeholder="输入文字，Enter 翻译，Shift + Enter 换行"
        autoSize={{ minRows: 1, maxRows: 4 }} value={draft} onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) {
            event.preventDefault(); submit();
          }
        }} />
      <Button type="text" htmlType="submit" icon={<SendOutlined />} aria-label="翻译输入文字" disabled={!draft.trim() || props.providerSaving} />
    </form>
    <Modal title="设置翻译引擎" open={settingsOpen} footer={null} onCancel={() => setSettingsOpen(false)}>
      <div className="pdf-translation-config">
        <label htmlFor="reader-translation-provider">默认机器翻译源</label>
        <Select id="reader-translation-provider" aria-label="默认机器翻译源" showSearch optionFilterProp="label"
          loading={props.providersLoading || props.providerSaving} disabled={props.providerSaving}
          value={props.providerGroupId} placeholder="请选择机器翻译源"
          options={props.providers.map((item) => {
            const name = translationProviderDisplayName(item.provider_name, language);
            return { value: item.group_id, label: item.group_name && item.group_name !== item.provider_name ? `${name} · ${item.group_name}` : name };
          })}
          onChange={props.onProviderChange} />
        <a href="/settings?section=model_defaults&service=translation">管理翻译服务和密钥</a>
      </div>
    </Modal>
  </section>;
}
