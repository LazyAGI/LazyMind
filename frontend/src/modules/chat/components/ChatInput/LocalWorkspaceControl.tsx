import { useEffect, useRef, useState } from "react";
import { Button, Modal, Select, Space, Tag, message } from "antd";
import { FolderOpenOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { getRuntimeMode } from "@/runtime/mode";
import {
  authorizeWorkspace,
  getConversationWorkspace,
  listWorkspaces,
  prepareWorkspaceReauthorization,
  revokeWorkspace,
  selectWorkspaceCandidate,
  updateWorkspacePermission,
  workspaceReason,
  type LocalWorkspaceView,
  type WorkspacePermissionMode,
} from "@/modules/chat/utils/localWorkspace";

interface Props {
  conversationId?: string;
  disabled?: boolean;
  onChange: (workspaceId: string | undefined, mode: WorkspacePermissionMode) => void;
}
export default function LocalWorkspaceControl({ conversationId, disabled, onChange }: Props) {
  const { t } = useTranslation();
  const runtime = getRuntimeMode();
  const labels: Record<WorkspacePermissionMode, string> = {
    always_ask: t("chat.workspace.everyAsk"), ask_as_needed: t("chat.workspace.askAsNeeded"), allow_all: t("chat.workspace.allowAll"),
  };
  const [items, setItems] = useState<LocalWorkspaceView[]>([]);
  const [selected, setSelected] = useState<LocalWorkspaceView>();
  const [mode, setMode] = useState<WorkspacePermissionMode>("ask_as_needed");
  const [candidate, setCandidate] = useState<{ token: string; name?: string; path?: string; reauthorization?: boolean }>();
  const [busy, setBusy] = useState(false);
  const onChangeRef = useRef(onChange);
  useEffect(() => { onChangeRef.current = onChange; }, [onChange]);

  useEffect(() => {
    let active = true;
    void (conversationId
      ? getConversationWorkspace(conversationId).then((workspace) => workspace ? [workspace] : [])
      : listWorkspaces()
    ).then((values) => {
      if (!active) return;
      setItems(values);
      if (values.length === 1 && conversationId) {
        setSelected(values[0]);
        setMode(values[0].permission_mode ?? "ask_as_needed");
        onChangeRef.current(values[0].workspace_id, values[0].permission_mode ?? "ask_as_needed");
      }
    }).catch(() => undefined);
    return () => { active = false; };
  }, [conversationId]);

  if (runtime !== "local" && runtime !== "desktop") return null;

  const choose = async () => {
    setBusy(true);
    try {
      const reauthorization = Boolean(conversationId && selected && selected.status !== "active");
      const result = reauthorization
        ? await prepareWorkspaceReauthorization(runtime, selected!.workspace_id)
        : await selectWorkspaceCandidate(runtime);
      if (!result.canceled && result.selection_token) {
        setCandidate({ token: result.selection_token, name: result.display_name, path: result.path, reauthorization });
      }
    } catch (error) {
      message.error(`${t("chat.workspace.chooseFailed")}：${workspaceReason(error)}`);
    } finally { setBusy(false); }
  };
  const allow = async () => {
    if (!candidate) return;
    setBusy(true);
    try {
      const workspace = await authorizeWorkspace(runtime, candidate.token);
      setItems((current) => [workspace, ...current.filter((item) => item.workspace_id !== workspace.workspace_id)]);
      if (!candidate.reauthorization) {
        setSelected(workspace);
        onChangeRef.current(workspace.workspace_id, mode);
      }
      setCandidate(undefined);
    } catch (error) { message.error(`${t("chat.workspace.authorizeFailed")}：${workspaceReason(error)}`); }
    finally { setBusy(false); }
  };
  const changeMode = async (next: WorkspacePermissionMode) => {
    if (next === "allow_all") {
      Modal.confirm({ title: t("chat.workspace.allowAllTitle"), content: t("chat.workspace.allowAllRisk"), onOk: () => void applyMode(next) });
      return;
    }
    await applyMode(next);
  };
  const applyMode = async (next: WorkspacePermissionMode) => {
    if (conversationId && selected?.permission_version) {
      setBusy(true);
      try {
        const result = await updateWorkspacePermission(conversationId, next, selected.permission_version);
        setSelected({ ...selected, permission_mode: result.permission_mode, permission_version: result.permission_version });
        setMode(result.permission_mode);
        onChangeRef.current(selected.workspace_id, result.permission_mode);
        message.success(t("chat.workspace.savedNext"));
      } catch (error) { message.error(`${t("chat.workspace.saveFailed")}：${workspaceReason(error)}`); }
      finally { setBusy(false); }
      return;
    }
    setMode(next); onChangeRef.current(selected?.workspace_id, next);
  };

  const revoke = () => {
    if (!selected?.version) return;
    Modal.confirm({ title: t("chat.workspace.revokeTitle"), content: t("chat.workspace.revokeAffected", { count: selected.affected_task_count ?? 0 }), okButtonProps: { danger: true }, onOk: async () => {
      setBusy(true);
      try {
        const result = await revokeWorkspace(selected.workspace_id, selected.version);
        setSelected({ ...selected, status: "revoked", version: result.version });
        onChangeRef.current(undefined, mode);
        message.success(result.stop_failed_count > 0 ? t("chat.workspace.revokedStopFailed") : t("chat.workspace.revoked"));
      } catch (error) { message.error(`${t("chat.workspace.revokeFailed")}：${workspaceReason(error)}`); }
      finally { setBusy(false); }
    }});
  };

  return <>
    <Space size={6} wrap>
      <Button size="small" icon={<FolderOpenOutlined />} disabled={disabled} loading={busy} onClick={() => void choose()}>
        {selected?.display_name ?? t("chat.workspace.select")}
      </Button>
      {items.length > 0 && !conversationId && <Select size="small" allowClear placeholder={t("chat.workspace.recent")} value={selected?.workspace_id}
        options={items.map((item) => ({ value: item.workspace_id, label: item.display_name, disabled: item.status !== "active" }))}
        onChange={(id) => { const workspace = items.find((item) => item.workspace_id === id); setSelected(workspace); onChangeRef.current(id, mode); }} />}
      {selected && <><Tag color={selected.status === "active" ? undefined : "error"}>{selected.path}</Tag><Select size="small" value={mode} disabled={disabled || busy || selected.status !== "active"}
        options={Object.entries(labels).map(([value, label]) => ({ value, label }))}
        onChange={(value) => void changeMode(value)} />
        {conversationId && selected.status === "active" && <Button size="small" danger disabled={busy} onClick={revoke}>{t("chat.workspace.revoke")}</Button>}
      </>}
    </Space>
    <Modal open={Boolean(candidate)} title={t("chat.workspace.authorizeTitle")} confirmLoading={busy} onCancel={() => setCandidate(undefined)} onOk={() => void allow()} okText={t("chat.workspace.authorize")}>
      <p>{candidate?.name}</p><p style={{ wordBreak: "break-all" }}>{candidate?.path}</p>
      <p>{t("chat.workspace.scope")}</p>
    </Modal>
  </>;
}
