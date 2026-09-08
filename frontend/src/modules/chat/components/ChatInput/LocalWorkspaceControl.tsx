import { useEffect, useRef, useState } from "react";
import { Button, Input, Modal, Select, Space, Tag, message } from "antd";
import { FolderOpenOutlined, SettingOutlined } from "@ant-design/icons";
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
  const [manageOpen, setManageOpen] = useState(false);
  const [managedItems, setManagedItems] = useState<LocalWorkspaceView[]>([]);
  const [busy, setBusy] = useState(false);
  const onChangeRef = useRef(onChange);
  const selectedRef = useRef<LocalWorkspaceView>();
  const requestRef = useRef(0);
  const listRequestRef = useRef(0);
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

  const reasonText = (error: unknown) => {
    const reason = workspaceReason(error);
    return t(`chat.workspace.reason.${reason}`, { defaultValue: t("chat.workspace.reason.unknown") });
  };
  const refreshWorkspaceState = async () => {
    const request = requestRef.current;
    if (conversationId) {
      const workspace = await getConversationWorkspace(conversationId).catch(() => undefined);
      if (request !== requestRef.current) return;
      selectedRef.current = workspace;
      setSelected(workspace);
      setItems(workspace ? [workspace] : []);
      const nextMode = workspace?.permission_mode ?? "ask_as_needed";
      setMode(nextMode);
      onChangeRef.current(workspace?.status === "active" ? workspace.workspace_id : undefined, nextMode);
      return;
    }
    const values = await listWorkspaces().catch(() => []);
    if (request !== requestRef.current) return;
    setItems(values);
    if (selectedRef.current && !values.some((item) => item.workspace_id === selectedRef.current?.workspace_id)) {
      selectedRef.current = undefined;
      setSelected(undefined);
      onChangeRef.current(undefined, mode);
    }
  };
  const refreshOnConflict = (error: unknown) => {
    if (["binding_conflict", "workspace_not_found", "revoked", "path_unavailable"].includes(workspaceReason(error))) {
      void refreshWorkspaceState();
    }
  };
  const loadManagedItems = async (query = "") => {
    const request = ++listRequestRef.current;
    try {
      const values = await listWorkspaces({ query, includeInactive: true });
      if (request === listRequestRef.current) setManagedItems(values);
    } catch (error) {
      if (request === listRequestRef.current) message.error(`${t("chat.workspace.loadFailed")}：${reasonText(error)}`);
    }
  };

  const choose = async (target = selected) => {
    const request = requestRef.current;
    setBusy(true);
    try {
      const reauthorization = Boolean(target && target.status !== "active");
      const result = reauthorization
        ? await prepareWorkspaceReauthorization(runtime, target!.workspace_id)
        : await selectWorkspaceCandidate(runtime);
      if (request !== requestRef.current) return;
      if (!result.canceled && result.selection_token) {
        setCandidate({ token: result.selection_token, name: result.display_name, path: result.path, reauthorization, conversationId });
      }
    } catch (error) {
      if (request !== requestRef.current) return;
      message.error(`${t("chat.workspace.chooseFailed")}：${reasonText(error)}`);
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
      setManagedItems((current) => [workspace, ...current.filter((item) => item.workspace_id !== workspace.workspace_id)]);
      if (!candidate.reauthorization) {
        selectedRef.current = workspace;
        setSelected(workspace);
        onChangeRef.current(workspace.workspace_id, mode);
      }
      setCandidate(undefined);
    } catch (error) {
      if (request === requestRef.current) {
        message.error(`${t("chat.workspace.authorizeFailed")}：${reasonText(error)}`);
        refreshOnConflict(error);
      }
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
        if (request === requestRef.current) {
          message.error(`${t("chat.workspace.saveFailed")}：${reasonText(error)}`);
          refreshOnConflict(error);
        }
      } finally {
        if (request === requestRef.current) setBusy(false);
      }
      return;
    }
    setMode(next);
    onChangeRef.current(selected?.workspace_id, next);
  };

  const revoke = (target = selected) => {
    if (!target?.version) return;
    Modal.confirm({ title: t("chat.workspace.revokeTitle"), content: t("chat.workspace.revokeAffected", { count: target.affected_task_count ?? 0 }), okButtonProps: { danger: true }, onOk: async () => {
      const request = requestRef.current;
      setBusy(true);
      try {
        const result = await revokeWorkspace(target.workspace_id, target.version);
        if (request !== requestRef.current) return;
        const workspace: LocalWorkspaceView = { ...target, status: "revoked", version: result.version };
        setItems((current) => current.filter((item) => item.workspace_id !== target.workspace_id));
        setManagedItems((current) => current.map((item) => item.workspace_id === target.workspace_id ? workspace : item));
        if (selectedRef.current?.workspace_id === target.workspace_id) {
          selectedRef.current = conversationId ? workspace : undefined;
          setSelected(conversationId ? workspace : undefined);
          onChangeRef.current(undefined, mode);
        }
        message.success(result.stop_failed_count > 0 ? t("chat.workspace.revokedStopFailed") : t("chat.workspace.revoked"));
      } catch (error) {
        if (request === requestRef.current) {
          message.error(`${t("chat.workspace.revokeFailed")}：${reasonText(error)}`);
          refreshOnConflict(error);
          if (manageOpen) void loadManagedItems();
        }
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
      {!conversationId && <Button size="small" icon={<SettingOutlined />} disabled={disabled || busy} onClick={() => { setManageOpen(true); void loadManagedItems(); }}>
        {t("chat.workspace.manage")}
      </Button>}
      {selected && <><Tag color={selected.status === "active" ? undefined : "error"}>{selected.path}</Tag><Select size="small" value={mode} disabled={busy || selected.status !== "active"}
        options={Object.entries(labels).map(([value, label]) => ({ value, label }))}
        onChange={(value) => void changeMode(value)} />
        {conversationId && selected.status === "active" && <Button size="small" danger disabled={busy} onClick={() => revoke()}>{t("chat.workspace.revoke")}</Button>}
      </>}
    </Space>
    {(!candidate || currentCandidate) && <Modal open={Boolean(currentCandidate)} title={t("chat.workspace.authorizeTitle")} confirmLoading={busy} onCancel={() => setCandidate(undefined)} onOk={() => void allow()} okText={t("chat.workspace.authorize")}>
      <p>{currentCandidate?.name}</p><p style={{ wordBreak: "break-all" }}>{currentCandidate?.path}</p>
      <p>{t("chat.workspace.scope")}</p>
    </Modal>}
    <Modal open={manageOpen} title={t("chat.workspace.manageTitle")} footer={null} onCancel={() => setManageOpen(false)}>
      <Input.Search allowClear placeholder={t("chat.workspace.search")} onSearch={(value) => void loadManagedItems(value)} />
      <Space direction="vertical" style={{ width: "100%", marginTop: 12 }}>
        {managedItems.map((item) => <Space key={item.workspace_id} style={{ justifyContent: "space-between", width: "100%" }}>
          <span><strong>{item.display_name}</strong><br /><small>{item.path}</small></span>
          <Space><Tag color={item.status === "active" ? "success" : "default"}>{t(`chat.workspace.status.${item.status}`)}</Tag>
            {item.status === "active"
              ? <Button size="small" danger onClick={() => revoke(item)}>{t("chat.workspace.revoke")}</Button>
              : <Button size="small" onClick={() => void choose(item)}>{t("chat.workspace.reauthorize")}</Button>}
          </Space>
        </Space>)}
      </Space>
    </Modal>
  </>;
}
