import { useEffect, useRef, useState } from "react";
import { Button, Drawer, Dropdown, Tooltip, message } from "antd";
import { FileAddOutlined, FileOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { useTaskCenterStore, type ConversationArtifact } from "@/modules/chat/store/taskCenter";
import { TaskServiceApi } from "@/modules/chat/utils/request";
import ArtifactCollectorCard from "../../ArtifactCollectorCard";
import MarkdownViewer from "../../MarkdownViewer";
import type { ChatExport } from "../types";
import "./ChatExportActions.scss";

import { exportCitationMarkdown } from "@/modules/chat/utils/chatExportCitations";
import type { ChatSourceCollection } from "@/modules/chat/utils/sourceAdapter";

interface Props {
  sources?: ChatSourceCollection;
  content: string;
  exports: ChatExport[];
  conversationId: string;
  historyId: string;
}

export default function ChatExportActions({ content, exports, conversationId, historyId, sources = [] }: Props) {
  const { t } = useTranslation();
  const artifacts = useTaskCenterStore((state) => state.artifactsByConversation[conversationId]);
  const load = useTaskCenterStore((state) => state.loadConversationArtifacts);
  const upsert = useTaskCenterStore((state) => state.upsertConversationArtifact);
  const [saving, setSaving] = useState<Record<string, boolean>>({});
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<ConversationArtifact | null>(null);
  const pending = useRef(new Set<string>());
  const revision = useRef(0);
  useEffect(() => { void load(conversationId); }, [conversationId, load]);
  useEffect(() => {
    setSelected(null);
    setOpen(false);
    return () => { revision.current += 1; };
  }, [conversationId, historyId, content]);

  const save = async (item: ChatExport) => {
    if (!artifacts || pending.current.has(item.export_id)) return;
    const saved = artifacts.find((artifact) => artifact.artifact_id === item.export_id);
    if (saved) {
      setSelected(null);
      setOpen(true);
      return;
    }
    const currentRevision = revision.current;
    pending.current.add(item.export_id);
    setSaving((previous) => ({ ...previous, [item.export_id]: true }));
    try {
      const response = await TaskServiceApi().createConversationArtifact(conversationId, {
        history_id: historyId, export_id: item.export_id, filename: item.filename,
        content_type: item.content_type, content: exportCitationMarkdown(content.slice(item.start, item.end), sources, window.location.origin),
      });
      const artifact = response.data?.data ?? response.data;
      if (!artifact?.artifact_id) throw new Error(t("chat.exportSaveFailed"));
      upsert(conversationId, artifact);
      if (revision.current === currentRevision) {
        setSelected(null);
        setOpen(true);
      }
    } catch (error) {
      message.error(getLocalizedErrorMessage(error) || t("chat.exportSaveFailed"));
    } finally {
      pending.current.delete(item.export_id);
      setSaving((previous) => ({ ...previous, [item.export_id]: false }));
    }
  };

  let cursor = 0;
  const validExports = exports.filter((item) => {
    if (item.start < cursor || item.end <= item.start || item.end > content.length) return false;
    cursor = item.end;
    return true;
  });

  if (validExports.length === 0) return null;
  const isSaved = (item: ChatExport) => artifacts?.some((artifact) => artifact.artifact_id === item.export_id);
  const allSaved = validExports.every(isSaved);
  const label = t(allSaved ? "chat.exportView" : "chat.exportSave");
  const multiple = validExports.length > 1;
  const buttonLabel = multiple ? label : `${label} · ${validExports[0].filename}`;
  const button = <Button className="tool-btn" aria-label={buttonLabel}
    icon={allSaved ? <FileOutlined /> : <FileAddOutlined />}
    disabled={!artifacts} loading={Object.values(saving).some(Boolean)}
    onClick={multiple ? undefined : () => void save(validExports[0])} />;

  return <>
    {multiple ? <Dropdown trigger={["click"]} menu={{
      items: validExports.map((item) => ({
        key: item.export_id,
        icon: isSaved(item) ? <FileOutlined /> : <FileAddOutlined />,
        label: <div className="chat-export-menu-item">
          <strong>{item.title}</strong>
          <span>{item.filename} · Markdown (.md)</span>
          <span>{t(isSaved(item) ? "chat.exportView" : "chat.exportSave")}</span>
        </div>,
      })),
      onClick: ({ key }) => {
        const item = validExports.find((candidate) => candidate.export_id === key);
        if (item) void save(item);
      },
    }}>
      <Tooltip title={buttonLabel}>{button}</Tooltip>
    </Dropdown> : <Tooltip title={buttonLabel}>{button}</Tooltip>}
    <Drawer open={open} onClose={() => { setOpen(false); setSelected(null); }}
      title={t("chat.exportView")} width="min(640px, 100vw)" destroyOnHidden
      className="chat-export-drawer">
      <ArtifactCollectorCard sessionId={conversationId} historyId={historyId}
        currentExportIds={validExports.map((item) => item.export_id)}
        previewArtifactId={selected?.artifact_id} onPreview={setSelected} />
      {selected && <section className="chat-export-drawer__preview" aria-label={t("chat.exportPreviewing")}>
        <div className="chat-export-drawer__summary">
          <strong>{selected.filename}</strong>
        </div>
        {selected.content_type === "json"
          ? <pre>{JSON.stringify(selected.value?.data ?? selected.value, null, 2)}</pre>
          : <MarkdownViewer>{selected.value?.text ?? ""}</MarkdownViewer>}
      </section>}
    </Drawer>
  </>;
}
