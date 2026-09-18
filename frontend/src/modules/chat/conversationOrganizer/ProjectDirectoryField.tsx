import { Button, Form, Modal } from "antd";
import { FolderOpenOutlined } from "@ant-design/icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { getRuntimeMode } from "@/runtime/mode";
import { authorizeWorkspace, selectWorkspaceCandidate } from "@/modules/chat/utils/localWorkspace";

export const defaultProjectName = (path: string) => {
  const trimmed = path.replace(/[\\/]+$/, "");
  if (!trimmed || /^[a-z]:$/i.test(trimmed) || /^\\\\[^\\]+\\[^\\]+$/.test(trimmed)) return path;
  return trimmed.split(/[\\/]/).pop() || path;
};

export default function ProjectDirectoryField() {
  const form = Form.useFormInstance();
  const { t } = useTranslation();
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const runtime = getRuntimeMode();
  const choose = async () => {
    if (runtime !== "local" && runtime !== "desktop") return;
    setBusy(true);
    try {
      const candidate = await selectWorkspaceCandidate(runtime);
      if (candidate.canceled || !candidate.selection_token) return;
      Modal.confirm({
        title: t("chat.workspace.authorizeTitle"),
        content: <><p>{candidate.path}</p><p>{t("chat.workspace.scope")}</p></>,
        okText: t("chat.workspace.authorize"), cancelText: t("common.cancel"),
        onOk: async () => {
          const workspace = await authorizeWorkspace(runtime, candidate.selection_token!);
          setPath(workspace.path);
          form.setFieldValue("workspace_id", workspace.workspace_id);
          if (!form.getFieldValue("name")?.trim()) form.setFieldValue("name", defaultProjectName(workspace.path));
        },
      });
    } finally { setBusy(false); }
  };
  return <>
    <Form.Item name="workspace_id" hidden rules={[{ required: true, message: t("conversationProject.directoryRequired") }]}><input /></Form.Item>
    <Form.Item label={t("conversationProject.directory")} required>
      <Button icon={<FolderOpenOutlined />} loading={busy} disabled={runtime !== "local" && runtime !== "desktop"} onClick={() => void choose()}>{t("chat.workspace.openFolder")}</Button>
      <p>{path || t("conversationProject.directoryRequired")}</p>
    </Form.Item>
  </>;
}
