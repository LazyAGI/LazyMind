import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { Button, Input, Modal, Popover, Select, Space, Tag, message, type SelectProps } from "antd";
import { CheckOutlined, CloseOutlined, DownOutlined, ExclamationCircleOutlined, FolderOpenOutlined, SafetyCertificateOutlined, SettingOutlined, StopOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { getRuntimeMode } from "@/runtime/mode";
import {
  authorizeWorkspace,
  decideWorkspaceApproval,
  getConversationWorkspace,
  listWorkspaces,
  listWorkspaceApprovals,
  prepareWorkspaceReauthorization,
  revokeWorkspace,
  selectWorkspaceCandidate,
  updateWorkspacePermission,
  workspaceReason,
  type LocalWorkspaceView,
  type WorkspaceApproval,
  type WorkspacePermissionMode,
} from "@/modules/chat/utils/localWorkspace";

interface Props {
  conversationId?: string;
  configResetKey?: number | string;
  disabled?: boolean;
  onSavingChange?: (saving: boolean) => void;
  onChange: (workspaceId: string | undefined, mode: WorkspacePermissionMode) => void;
}
export default function LocalWorkspaceControl({ conversationId, configResetKey, disabled, onChange, onSavingChange }: Props) {
  const { t } = useTranslation();
  const runtime = getRuntimeMode();
  const labels: Record<WorkspacePermissionMode, string> = {
    always_ask: t("chat.workspace.everyAsk"), ask_as_needed: t("chat.workspace.askAsNeeded"), allow_all: t("chat.workspace.allowAll"),
  };
  const permissionDescriptions: Record<WorkspacePermissionMode, string> = {
    always_ask: t("chat.workspace.everyAskDescription"),
    ask_as_needed: t("chat.workspace.askAsNeededDescription"),
    allow_all: t("chat.workspace.allowAllDescription"),
  };
  const [items, setItems] = useState<LocalWorkspaceView[]>([]);
  const [selected, setSelected] = useState<LocalWorkspaceView>();
  const [mode, setMode] = useState<WorkspacePermissionMode>("ask_as_needed");
  const [candidate, setCandidate] = useState<{ token: string; name?: string; path?: string; reauthorization?: boolean; conversationId?: string }>();
  const [manageOpen, setManageOpen] = useState(false);
  const [workspaceMenuOpen, setWorkspaceMenuOpen] = useState(false);
  const [allowAllOpen, setAllowAllOpen] = useState(false);
  const [allowAllRequest, setAllowAllRequest] = useState<number>();
  const [workspaceQuery, setWorkspaceQuery] = useState("");
  const [managedItems, setManagedItems] = useState<LocalWorkspaceView[]>([]);
  const [initializationError, setInitializationError] = useState(false);
  const onSavingChangeRef = useRef(onSavingChange);
  onSavingChangeRef.current = onSavingChange;
  const [busy, setBusy] = useState(false);
  const [approvals, setApprovals] = useState<{ conversationId: string; items: WorkspaceApproval[] }>();
  const [approvalsOpen, setApprovalsOpen] = useState<string>();
  const [approvalBusy, setApprovalBusy] = useState<string>();
  const [approvalError, setApprovalError] = useState<string>();
  const approvalRequestRef = useRef(0);
  const dismissedApprovalIdsRef = useRef(new Set<string>());
  const revokeConfirmRef = useRef<ReturnType<typeof Modal.confirm>>();
  const refreshApprovalsRef = useRef(() => {});
  const onChangeRef = useRef(onChange);
  const selectedRef = useRef<LocalWorkspaceView>();
  const requestRef = useRef(0);
  const workspaceLoadRequestRef = useRef(0);
  const listRequestRef = useRef(0);
  const conversationRef = useRef(conversationId);
  const resetKeyRef = useRef(configResetKey);
  if (conversationRef.current !== conversationId || resetKeyRef.current !== configResetKey) {
    conversationRef.current = conversationId;
    resetKeyRef.current = configResetKey;
    requestRef.current += 1;
  }
  useEffect(() => { onChangeRef.current = onChange; }, [onChange]);
  useEffect(() => { selectedRef.current = selected; }, [selected]);

  useEffect(() => {
    const hadSelection = Boolean(selectedRef.current);
    selectedRef.current = undefined;
    setItems([]);
    setSelected(undefined);
    setMode("ask_as_needed");
    setCandidate(undefined);
    listRequestRef.current += 1;
    setManageOpen(false);
    setWorkspaceMenuOpen(false);
    setAllowAllOpen(false);
    setAllowAllRequest(undefined);
    setWorkspaceQuery("");
    setManagedItems([]);
    setBusy(false);
    onSavingChangeRef.current?.(false);
    setInitializationError(false);
    dismissedApprovalIdsRef.current.clear();
    revokeConfirmRef.current?.destroy();
    revokeConfirmRef.current = undefined;
    if (hadSelection) onChangeRef.current(undefined, "ask_as_needed");
    if (runtime !== "local" && runtime !== "desktop") return;
    void refreshWorkspaceState();
    return () => {
      onSavingChangeRef.current?.(false);
      requestRef.current += 1;
      listRequestRef.current += 1;
      revokeConfirmRef.current?.destroy();
      revokeConfirmRef.current = undefined;
    };
  }, [conversationId, configResetKey, runtime]);

  useEffect(() => {
    setApprovals(undefined);
    setApprovalsOpen(undefined);
    setApprovalBusy(undefined);
    setApprovalError(undefined);
    if (!conversationId || !selected?.workspace_id || (runtime !== "local" && runtime !== "desktop")) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    let controller: AbortController | undefined;
    const visible = () => document.visibilityState !== "hidden";
    const refresh = async () => {
      clearTimeout(timer);
      controller?.abort();
      const request = ++approvalRequestRef.current;
      if (!active || !visible()) return;
      controller = new AbortController();
      try {
        const values = await listWorkspaceApprovals(conversationId, controller.signal);
        if (!active || request !== approvalRequestRef.current || conversationId !== conversationRef.current) return;
        setApprovals({ conversationId, items: values });
        const pendingIds = new Set(values.filter((item) => item.status === "pending").map((item) => item.operation_id));
        for (const id of dismissedApprovalIdsRef.current) {
          if (!pendingIds.has(id)) dismissedApprovalIdsRef.current.delete(id);
        }
        if ([...pendingIds].some((id) => !dismissedApprovalIdsRef.current.has(id))) setApprovalsOpen(conversationId);
        setApprovalError(undefined);
      } catch (error) {
        if (active && request === approvalRequestRef.current && conversationId === conversationRef.current && !controller.signal.aborted) setApprovalError(workspaceReason(error));
      } finally {
        if (active && request === approvalRequestRef.current && conversationId === conversationRef.current && visible()) timer = setTimeout(refresh, 1000);
      }
    };
    const onVisibility = () => void refresh();
    refreshApprovalsRef.current = onVisibility;
    document.addEventListener("visibilitychange", onVisibility);
    void refresh();
    return () => {
      active = false;
      approvalRequestRef.current += 1;
      clearTimeout(timer);
      controller?.abort();
      refreshApprovalsRef.current = () => {};
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [conversationId, selected?.workspace_id, runtime]);

  if (runtime !== "local" && runtime !== "desktop") return null;

  const reasonText = (error: unknown) => {
    const reason = workspaceReason(error);
    return t(`chat.workspace.reason.${reason}`, { defaultValue: t("chat.workspace.reason.unknown") });
  };
  const refreshWorkspaceState = async () => {
    const request = requestRef.current;
    const loadRequest = ++workspaceLoadRequestRef.current;
    try {
      if (conversationId) {
        const workspace = await getConversationWorkspace(conversationId);
        if (request !== requestRef.current || loadRequest !== workspaceLoadRequestRef.current) return;
        const hadSelection = Boolean(selectedRef.current);
        setInitializationError(false);
        selectedRef.current = workspace;
        setSelected(workspace);
        setItems(workspace ? [workspace] : []);
        const nextMode = workspace?.permission_mode ?? "ask_as_needed";
        setMode(nextMode);
        if (workspace || hadSelection) {
          onChangeRef.current(workspace?.status === "active" ? workspace.workspace_id : undefined, nextMode);
        }
        return;
      }
      const values = await listWorkspaces();
      if (request !== requestRef.current || loadRequest !== workspaceLoadRequestRef.current) return;
      setInitializationError(false);
      setItems(values);
      if (selectedRef.current && !values.some((item) => item.workspace_id === selectedRef.current?.workspace_id)) {
        selectedRef.current = undefined;
        setSelected(undefined);
        onChangeRef.current(undefined, mode);
      }
    } catch {
      if (request !== requestRef.current || loadRequest !== workspaceLoadRequestRef.current) return;
      setInitializationError(true);
    }
  };
  const refreshOnConflict = (error: unknown) => {
    if (["binding_conflict", "workspace_not_found", "revoked", "path_unavailable"].includes(workspaceReason(error))) {
      void refreshWorkspaceState();
    }
  };
  const retryWorkspaceState = () => {
    void refreshWorkspaceState();
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
      setAllowAllRequest(requestRef.current);
      setAllowAllOpen(true);
      return;
    }
    await applyMode(next);
  };
  const applyMode = async (next: WorkspacePermissionMode) => {
    if (conversationId && selected?.permission_version) {
      const request = requestRef.current;
      setBusy(true);
      onSavingChangeRef.current?.(true);
      try {
        const result = await updateWorkspacePermission(conversationId, next, selected.permission_version);
        if (request !== requestRef.current) return;
        workspaceLoadRequestRef.current += 1;
        setInitializationError(false);
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
        if (request === requestRef.current) {
          setBusy(false);
          onSavingChangeRef.current?.(false);
        }
      }
      return;
    }
    setMode(next);
    onChangeRef.current(selected?.workspace_id, next);
  };

  const revoke = (target = selected) => {
    if (!target?.version) return;
    const request = requestRef.current;
    revokeConfirmRef.current?.destroy();
    revokeConfirmRef.current = Modal.confirm({ title: t("chat.workspace.revokeTitle"), content: t("chat.workspace.revokeAffected", { count: target.affected_task_count ?? 0 }), okButtonProps: { danger: true }, onOk: async () => {
      if (request !== requestRef.current) return;
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

  const decideApproval = async (item: WorkspaceApproval, action: "allow_once" | "reject") => {
    if (!conversationId || conversationId !== conversationRef.current || item.status !== "pending" || approvalBusy) return;
    const request = requestRef.current;
    setApprovalBusy(item.operation_id);
    try {
      const result = await decideWorkspaceApproval(conversationId, item.operation_id, action);
      if (request === requestRef.current) {
        dismissedApprovalIdsRef.current.add(item.operation_id);
        setApprovals((current) => current && current.conversationId === conversationId
          ? { ...current, items: current.items.map((value) => value.operation_id === item.operation_id ? { ...value, status: result.status } : value) }
          : current);
        if (!currentApprovals.some((value) => value.operation_id !== item.operation_id && value.status === "pending")) {
          setApprovalsOpen(undefined);
        }
      }
    } catch (error) {
      if (request === requestRef.current) message.error(`${t("chat.workspace.approval.decisionFailed")}：${reasonText(error)}`);
    } finally {
      if (request === requestRef.current) { setApprovalBusy(undefined); refreshApprovalsRef.current(); }
    }
  };

  const currentApprovals = approvals && approvals.conversationId === conversationId ? approvals.items : [];
  const pendingApprovalCount = currentApprovals.filter((item) => item.status === "pending").length;
  const currentCandidate = candidate?.conversationId === conversationId ? candidate : undefined;
  const visibleItems = items.filter((item) => {
    const query = workspaceQuery.trim().toLowerCase();
    return !query || item.display_name.toLowerCase().includes(query) || item.path.toLowerCase().includes(query);
  });
  const selectDraftWorkspace = (workspace?: LocalWorkspaceView) => {
    selectedRef.current = workspace;
    setSelected(workspace);
    setWorkspaceMenuOpen(false);
    onChangeRef.current(workspace?.workspace_id, mode);
  };

  return <>
    <Space className="local-workspace-control" size={6} wrap>
      {initializationError && <Space>
        <span role="alert">{t("chat.workspace.loadFailed")}</span>
        <Button size="small" disabled={busy} onClick={retryWorkspaceState}>{t("chat.workspace.retry")}</Button>
      </Space>}
      {conversationId && selected && pendingApprovalCount > 0 && <Button size="small" onClick={() => setApprovalsOpen(conversationId)}>
        {t("chat.workspace.approval.open")} ({pendingApprovalCount})
      </Button>}
      {!conversationId && <Popover trigger="click" placement="bottomLeft" autoAdjustOverflow={false} open={workspaceMenuOpen} onOpenChange={setWorkspaceMenuOpen}
        content={<div className="local-workspace-menu">
          <Input.Search allowClear value={workspaceQuery} placeholder={t("chat.workspace.searchShort")} onChange={(event: ChangeEvent<HTMLInputElement>) => setWorkspaceQuery(event.target.value)} />
          <div className="local-workspace-menu-list">
            {visibleItems.map((item) => <button type="button" key={item.workspace_id} disabled={item.status !== "active"} onClick={() => selectDraftWorkspace(item)}>
              <span className="local-workspace-menu-icon"><FolderOpenOutlined /></span><span><strong>{item.display_name}</strong><small>{item.path}</small></span>
            </button>)}
          </div>
          <div className="local-workspace-menu-actions">
            <button type="button" onClick={() => { setWorkspaceMenuOpen(false); void choose(); }}><span className="local-workspace-menu-icon"><FolderOpenOutlined /></span>{t("chat.workspace.openFolder")}</button>
            <button type="button" onClick={() => selectDraftWorkspace()}><span className="local-workspace-menu-icon"><CloseOutlined /></span>{t("chat.workspace.none")}</button>
            <button type="button" onClick={() => { setWorkspaceMenuOpen(false); setManageOpen(true); void loadManagedItems(); }}><span className="local-workspace-menu-icon"><SettingOutlined /></span>{t("chat.workspace.manage")}</button>
          </div>
        </div>}>
        <Button className="local-workspace-trigger" size="small" icon={<FolderOpenOutlined />} disabled={disabled || busy} loading={busy}>
          {selected?.display_name ?? t("chat.workspace.select")} <DownOutlined />
        </Button>
      </Popover>}
      {(!conversationId || selected) && <><Select className="local-workspace-permission" size="small" value={mode} disabled={busy || !selected || Boolean(!conversationId && disabled) || selected.status !== "active"}
        classNames={{ popup: { root: "local-workspace-permission-menu" } }}
        options={Object.entries(labels).map(([value, label]) => ({ value, label }))}
        labelRender={(({ value }) => <Space size={6}><SafetyCertificateOutlined />{labels[value as WorkspacePermissionMode]}</Space>) as NonNullable<SelectProps<WorkspacePermissionMode>["labelRender"]>}
        optionRender={((option) => { const value = option.value as WorkspacePermissionMode; return <div className="local-workspace-permission-option">
          <span className="local-workspace-permission-icon">{value === "always_ask" ? <StopOutlined /> : value === "ask_as_needed" ? <SafetyCertificateOutlined /> : <ExclamationCircleOutlined />}</span>
          <span><strong>{labels[value]}</strong><small>{permissionDescriptions[value]}</small></span>
          {mode === value && <CheckOutlined className="local-workspace-permission-check" />}
        </div>; }) as NonNullable<SelectProps<WorkspacePermissionMode>["optionRender"]>}
        onChange={(value: WorkspacePermissionMode) => void changeMode(value)} />
      </>}
    </Space>
    {(!candidate || currentCandidate) && <Modal className="local-workspace-authorize-modal" open={Boolean(currentCandidate)} title={t("chat.workspace.authorizeTitle")} confirmLoading={busy} onCancel={() => setCandidate(undefined)} onOk={() => void allow()} okText={t("chat.workspace.authorize")}>
      <p className="local-workspace-authorize-question">{t("chat.workspace.authorizeQuestion", { name: currentCandidate?.name })}</p>
      <div className="local-workspace-authorize-folder"><FolderOpenOutlined /><span><strong>{currentCandidate?.name}</strong><small>{currentCandidate?.path}</small></span></div>
      <p className="local-workspace-authorize-scope">{t("chat.workspace.scope")}</p>
    </Modal>}
    <Modal className="local-workspace-allow-all-modal" open={allowAllOpen} title={t("chat.workspace.allowAllTitle")} okText={t("chat.workspace.allowAllConfirm")}
      okButtonProps={{ danger: true }} onCancel={() => setAllowAllOpen(false)} onOk={() => { setAllowAllOpen(false); if (allowAllRequest === requestRef.current) void applyMode("allow_all"); }}>
      <p>{t("chat.workspace.allowAllIntro")}</p>
      <div className="local-workspace-risk-list">
        <div><FolderOpenOutlined /><span><strong>{t("chat.workspace.allowAllFiles")}</strong><small>{t("chat.workspace.allowAllFilesDescription")}</small></span></div>
        <div><SafetyCertificateOutlined /><span><strong>{t("chat.workspace.allowAllProtected")}</strong><small>{t("chat.workspace.allowAllProtectedDescription")}</small></span></div>
        <div><StopOutlined /><span><strong>{t("chat.workspace.allowAllDestructive")}</strong><small>{t("chat.workspace.allowAllDestructiveDescription")}</small></span></div>
      </div>
      <p className="local-workspace-risk-warning"><ExclamationCircleOutlined />{t("chat.workspace.allowAllRisk")}</p>
    </Modal>
    <Modal open={Boolean(conversationId && approvalsOpen === conversationId && selected)} title={t("chat.workspace.approval.title")} footer={null} onCancel={() => {
      for (const item of currentApprovals) if (item.status === "pending") dismissedApprovalIdsRef.current.add(item.operation_id);
      setApprovalsOpen(undefined);
    }}>
      <p>{t("chat.workspace.approval.notice")}</p>
      {approvalError && <p role="alert">{t("chat.workspace.approval.loadFailed")}：{t(`chat.workspace.reason.${approvalError}`, { defaultValue: t("chat.workspace.reason.unknown") })}</p>}
      <Space direction="vertical" style={{ width: "100%" }}>
        {currentApprovals.map((item) => <section key={item.operation_id} style={{ width: "100%", padding: "12px 0", borderTop: "1px solid var(--ant-color-border-secondary, #d9d9d9)", overflowWrap: "anywhere" }}>
          <Space wrap><strong>{t(`chat.workspace.approval.operation.${item.operation}`, { defaultValue: item.operation })}</strong><Tag>{t(`chat.workspace.approval.status.${item.status}`, { defaultValue: t("chat.workspace.approval.status.unknown") })}</Tag></Space>
          <p>{item.path}</p>
          {item.status === "pending" && <Space>
            <Button type="primary" loading={approvalBusy === item.operation_id} disabled={Boolean(approvalBusy || approvalError) || item.expires_at <= Date.now()} onClick={() => void decideApproval(item, "allow_once")}>{t("chat.workspace.approval.allowOnce")}</Button>
            <Button danger disabled={Boolean(approvalBusy || approvalError) || item.expires_at <= Date.now()} onClick={() => void decideApproval(item, "reject")}>{t("chat.workspace.approval.reject")}</Button>
          </Space>}
        </section>)}
      </Space>
    </Modal>
    <Modal open={manageOpen} title={t("chat.workspace.manageTitle")} footer={null} onCancel={() => setManageOpen(false)}>
      <Input.Search allowClear placeholder={t("chat.workspace.search")} onSearch={(value: string) => void loadManagedItems(value)} />
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
