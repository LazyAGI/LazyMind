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
  const [candidate, setCandidate] = useState<{ token: string; name?: string; path?: string; reauthorization?: boolean; conversationId?: string }>();
  const [busy, setBusy] = useState(false);
  const onChangeRef = useRef(onChange);
  const selectedRef = useRef<LocalWorkspaceView>();
  const requestRef = useRef(0);
  const conversationRef = useRef(conversationId);
  if (conversationRef.current !== conversationId) {
    conversationRef.current = conversationId;
    requestRef.current += 1;
  }
  useEffect(() => { onChangeRef.current = onChange; }, [onChange]);
  useEffect(() => { selectedRef.current = selected; }, [selected]);

  useEffect(() => {
    const request = requestRef.current;
    let active = true;
    const hadSelection = Boolean(selectedRef.current);
    selectedRef.current = undefined;
    setItems([]);
    setSelected(undefined);
    setMode("ask_as_needed");
    setCandidate(undefined);
    setBusy(false);
    if (hadSelection) onChangeRef.current(undefined, "ask_as_needed");
    void (conversationId
      ? getConversationWorkspace(conversationId).then((workspace) => workspace ? [workspace] : [])
      : listWorkspaces()
    ).then((values) => {
      if (!active || request !== requestRef.current) return;
      setItems(values);
      if (values.length === 1 && conversationId) {
        selectedRef.current = values[0];
        setSelected(values[0]);
        setMode(values[0].permission_mode ?? "ask_as_needed");
        onChangeRef.current(values[0].workspace_id, values[0].permission_mode ?? "ask_as_needed");
      }
    }).catch(() => undefined);
    return () => { active = false; };
  }, [conversationId]);

  if (runtime !== "local" && runtime !== "desktop") return null;

  const choose = async () => {
    const request = requestRef.current;
    setBusy(true);
    try {
      const reauthorization = Boolean(conversationId && selected && selected.status !== "active");
      const result = reauthorization
        ? await prepareWorkspaceReauthorization(runtime, selected!.workspace_id)
        : await selectWorkspaceCandidate(runtime);
      if (request !== requestRef.current) return;
      if (!result.canceled && result.selection_token) {
        setCandidate({ token: result.selection_token, name: result.display_name, path: result.path, reauthorization, conversationId });
      }
    } catch (error) {
      if (request !== requestRef.current) return;
      message.error(`${t("chat.workspace.chooseFailed")}：${workspaceReason(error)}`);
    } finally {
      if (request === requestRef.current) setBusy(false);
    }
  };
  const allow = async () => {
    if (!candidate) return;
    const request = requestRef.current;
    setBusy(true);
    try {
      const workspace = await authorizeWorkspace(runtime, candidate.token);
      if (request !== requestRef.current) return;
      setItems((current) => [workspace, ...current.filter((item) => item.workspace_id !== workspace.workspace_id)]);
      if (!candidate.reauthorization) {
        selectedRef.current = workspace;
        setSelected(workspace);
        onChangeRef.current(workspace.workspace_id, mode);
      }
      setCandidate(undefined);
    } catch (error) {
      if (request === requestRef.current) message.error(`${t("chat.workspace.authorizeFailed")}：${workspaceReason(error)}`);
    } finally {
      if (request === requestRef.current) setBusy(false);
    }
  };
  const changeMode = async (next: WorkspacePermissionMode) => {
    if (next === "allow_all") {
      const request = requestRef.current;
      Modal.confirm({ title: t("chat.workspace.allowAllTitle"), content: t("chat.workspace.allowAllRisk"), onOk: () => {
        if (request === requestRef.current) void applyMode(next);
      } });
      return;
    }
    await applyMode(next);
  };
  const applyMode = async (next: WorkspacePermissionMode) => {
    if (conversationId && selected?.permission_version) {
      const request = requestRef.current;
      setBusy(true);
      try {
        const result = await updateWorkspacePermission(conversationId, next, selected.permission_version);
        if (request !== requestRef.current) return;
        const workspace = { ...selected, permission_mode: result.permission_mode, permission_version: result.permission_version };
        selectedRef.current = workspace;
        setSelected(workspace);
        setMode(result.permission_mode);
        onChangeRef.current(selected.workspace_id, result.permission_mode);
        message.success(t("chat.workspace.savedNext"));
      } catch (error) {
        if (request === requestRef.current) message.error(`${t("chat.workspace.saveFailed")}：${workspaceReason(error)}`);
      } finally {
        if (request === requestRef.current) setBusy(false);
      }
      return;
    }
    setMode(next);
    onChangeRef.current(selected?.workspace_id, next);
  };

  const revoke = () => {
    if (!selected?.version) return;
    Modal.confirm({ title: t("chat.workspace.revokeTitle"), content: t("chat.workspace.revokeAffected", { count: selected.affected_task_count ?? 0 }), okButtonProps: { danger: true }, onOk: async () => {
      const request = requestRef.current;
      setBusy(true);
      try {
        const result = await revokeWorkspace(selected.workspace_id, selected.version);
        if (request !== requestRef.current) return;
        const workspace = { ...selected, status: "revoked", version: result.version };
        selectedRef.current = workspace;
        setSelected(workspace);
        onChangeRef.current(undefined, mode);
        message.success(result.stop_failed_count > 0 ? t("chat.workspace.revokedStopFailed") : t("chat.workspace.revoked"));
      } catch (error) {
        if (request === requestRef.current) message.error(`${t("chat.workspace.revokeFailed")}：${workspaceReason(error)}`);
      } finally {
        if (request === requestRef.current) setBusy(false);
      }
    }});
  };

  const folderLocked = Boolean(conversationId && (!selected || selected.status === "active"));
  const currentCandidate = candidate?.conversationId === conversationId ? candidate : undefined;

  return <>
    <Space size={6} wrap>
      <Button size="small" icon={<FolderOpenOutlined />} disabled={disabled || busy || folderLocked} loading={busy} onClick={() => void choose()}>
        {selected?.display_name ?? t("chat.workspace.select")}
      </Button>
      {items.length > 0 && !conversationId && <Select size="small" allowClear placeholder={t("chat.workspace.recent")} value={selected?.workspace_id}
        disabled={disabled || busy}
        options={items.map((item) => ({ value: item.workspace_id, label: item.display_name, disabled: item.status !== "active" }))}
        onChange={(id) => { const workspace = items.find((item) => item.workspace_id === id); selectedRef.current = workspace; setSelected(workspace); onChangeRef.current(id, mode); }} />}
      {selected && <><Tag color={selected.status === "active" ? undefined : "error"}>{selected.path}</Tag><Select size="small" value={mode} disabled={busy || selected.status !== "active"}
        options={Object.entries(labels).map(([value, label]) => ({ value, label }))}
        onChange={(value) => void changeMode(value)} />
        {conversationId && selected.status === "active" && <Button size="small" danger disabled={busy} onClick={revoke}>{t("chat.workspace.revoke")}</Button>}
      </>}
    </Space>
    {(!candidate || currentCandidate) && <Modal open={Boolean(currentCandidate)} title={t("chat.workspace.authorizeTitle")} confirmLoading={busy} onCancel={() => setCandidate(undefined)} onOk={() => void allow()} okText={t("chat.workspace.authorize")}>
      <p>{currentCandidate?.name}</p><p style={{ wordBreak: "break-all" }}>{currentCandidate?.path}</p>
      <p>{t("chat.workspace.scope")}</p>
    </Modal>}
  </>;
}
