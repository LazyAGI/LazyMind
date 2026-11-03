import { useState } from "react";
import { Button, Input, Modal, Select, Spin, Tooltip } from "antd";
import { ArrowUpOutlined, SettingOutlined, DownOutlined } from "@ant-design/icons";
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
  onRetranslateWithLLM: () => void;
}

export default function PaperTranslationPanel(props: Props) {
  const { i18n } = useTranslation();
  const language = i18n.resolvedLanguage || i18n.language || "zh-CN";
  const [draft, setDraft] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const provider = props.providers.find(item => item.group_id === props.providerGroupId);
  const providerName = provider ? translationProviderDisplayName(provider.provider_name, language) : "设置翻译引擎";
  const submit = () => {
    const text = draft.trim();
    if (!text || props.providerSaving) return;
    setDraft("");
    props.onTranslate(text);
  };
  return <section className="knowledge-reader-translation" aria-label="论文翻译">
    <div className="paper-assistant-ready"><span aria-hidden="true"/><span>{props.loading ? "正在翻译" : "选中文字，即可翻译"}</span><span className="paper-assistant-ready__scope">原文对照</span></div>
    <div className="knowledge-reader-translation-content">
    <section className="knowledge-reader-translation-pane is-result" aria-label="翻译结果" aria-live="polite" aria-busy={props.loading}>
      <div className="knowledge-reader-translation-heading">
        <h3>译文</h3>
        <Button type="text" aria-label="用大模型重新翻译" size="small" disabled={!props.source.trim() || props.loading}
          onClick={props.onRetranslateWithLLM}>用大模型重新翻译</Button>
      </div>
      {props.loading ? <Spin tip="正在翻译…"><div className="knowledge-reader-translation-loading" /></Spin>
        : props.result ? <div className="knowledge-translation-text">{props.result}</div>
        : <div className="paper-assistant-welcome knowledge-reader-translation-welcome"><h2>读懂每一段原文</h2><p>选中论文中的文字后点击翻译，或在下方输入。译文与原文会显示在这里，方便对照阅读。</p></div>}
    </section>
    {props.source && <section className="knowledge-reader-translation-pane is-source" aria-label="翻译原文">
      <h3>原文</h3>
      <div className="knowledge-translation-text">{props.source}</div>
    </section>}
    </div>
    <form className="knowledge-reader-translation-composer" onSubmit={(event) => { event.preventDefault(); submit(); }}>
      <Input.TextArea aria-label="输入要翻译的文字" placeholder="输入要翻译的文字，或在论文中选中文字…"
        autoSize={{ minRows: 2, maxRows: 6 }} value={draft} onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) {
            event.preventDefault(); submit();
          }
        }} />
      <div className="knowledge-reader-translation-composer-actions">
        <Tooltip title="设置翻译引擎" placement="top"><Button className="knowledge-reader-translation-provider" aria-label="设置翻译引擎" icon={<SettingOutlined />} loading={props.providerSaving} onClick={() => setSettingsOpen(true)}><span>{providerName}</span><DownOutlined/></Button></Tooltip>
        <Tooltip title="翻译 · Enter" placement="top"><Button className="knowledge-reader-translation-send" type="primary" shape="circle" htmlType="submit" icon={<ArrowUpOutlined />} aria-label="翻译输入文字" disabled={!draft.trim() || props.providerSaving} /></Tooltip>
      </div>
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
