import { Input, Space, Tag } from "antd";
import { FolderOpenOutlined } from "@ant-design/icons";
import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { useTranslation } from "react-i18next";
import { listConversationGroups, type ConversationGroup } from "./api";
import { defaultProjectName } from "./ProjectDirectoryField";
import type { LocalWorkspaceView } from "../utils/localWorkspace";

export default function DraftProject({ workspace, fixedProject, initialName, onChange }: {
 workspace: LocalWorkspaceView; fixedProject?: ConversationGroup; initialName?: string;
 onChange: (name: string | undefined, valid: boolean) => void;
}) {
 const { t } = useTranslation();
 const [project, setProject] = useState<ConversationGroup>();
 const [name, setName] = useState("");
 const [failed, setFailed] = useState(false);
 const [retry, setRetry] = useState(0);
 const callback = useRef(onChange); callback.current = onChange;
 useEffect(() => {
  let disposed = false;
  callback.current(undefined, false); setFailed(false);
  void listConversationGroups().then(groups => {
   if (disposed) return;
   const existing = fixedProject || groups.find(group => group.kind === "project" && group.path === workspace.path);
   const nextName = existing?.name || initialName || defaultProjectName(workspace.path);
   setProject(existing); setName(nextName);
   callback.current(existing ? undefined : nextName, workspace.status === "active" && (Boolean(existing) || Array.from(nextName.trim()).length > 0 && Array.from(nextName.trim()).length <= 255));
  }).catch(() => { if (!disposed) setFailed(true); });
  return () => { disposed = true; };
 }, [workspace.workspace_id, workspace.path, workspace.status, fixedProject, initialName, retry]);
 return <Space wrap title={workspace.path}>
  <Tag icon={<FolderOpenOutlined />}>{t("conversationProject.project")}</Tag>
  {failed ? <button onClick={() => setRetry(value => value + 1)}>{t("conversationOrganizer.retryLoad")}</button> : project ? <span>{project.name}</span> : <Input aria-label={t("conversationProject.name")} value={name} status={!name.trim() || Array.from(name.trim()).length > 255 ? "error" : undefined} onChange={(event: ChangeEvent<HTMLInputElement>) => {
   setName(event.target.value);
   const value = event.target.value.trim(); callback.current(value, workspace.status === "active" && Boolean(value) && Array.from(value).length <= 255);
  }} />}
  <small>{workspace.path}</small>
  {!project && !failed && <small>{t("conversationProject.createOnSend")}</small>}
 </Space>;
}
