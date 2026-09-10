import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  HighlightOutlined,
  LoadingOutlined,
  UndoOutlined,
} from "@ant-design/icons";
import { Button, Drawer, Form, Modal, Select, Spin, Tooltip, message } from "antd";
import OrganizerSteps from "./OrganizerSteps";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  correctOrganizerItem,
  createConversationGroup,
  deleteConversationGroup,
  getLatestOrganizerState,
  getLatestSuccessfulOrganizerRun,
  getOrganizerRun,
  listConversationGroups,
  runAction,
  startOrganizerRun,
  updateConversationGroup,
  type ConversationGroup,
  type OrganizerRun,
  CONVERSATION_GROUPS_CHANGED_EVENT,
  emitConversationGroupsChanged,
} from "./api";
import "./index.scss";
import GroupFields, { normalizeGroupValues } from "./GroupFields";
import SidebarGroups from "./SidebarGroups";

const activeStatuses = new Set(["pending", "running", "applying"]);

type Props = {
  onChanged?: () => void;
  onNewChatInGroup?: (groupId: string) => void;
  mode?: "groups" | "organizer" | "all";
  searchText?: string;
  currentConversationId?: string;
};

export default function ConversationGroups({ onChanged, onNewChatInGroup, mode = "all", searchText, currentConversationId }: Props) {
  const { t } = useTranslation();
  const [groups, setGroups] = useState<ConversationGroup[]>([]);
  const [loading, setLoading] = useState(false);
  const [editing, setEditing] = useState<ConversationGroup | "new" | null>(null);
  const [editingFromRun, setEditingFromRun] = useState(false);
  const [correctingItemId, setCorrectingItemId] = useState("");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [resultLoading, setResultLoading] = useState(false);
  const [resultLoadFailed, setResultLoadFailed] = useState(false);
  const [run, setRun] = useState<OrganizerRun | null>(null);
  const [activeRun, setActiveRun] = useState<OrganizerRun | null>(null);
  const [freeCount, setFreeCount] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const [hasRecentResult, setHasRecentResult] = useState(false);
  const [form] = Form.useForm<{ name: string; scope?: string }>();
  const [correctionForm] = Form.useForm<{ name: string; scope?: string }>();
  const pollRef = useRef<number>();
  const startingRef = useRef(false);
  const cancelConfirmationRef = useRef(false);
  const pollGenerationRef = useRef(0);
  const onChangedRef = useRef(onChanged);
  useEffect(() => { onChangedRef.current = onChanged; }, [onChanged]);
  const lockedConversationIds = new Set(activeRun && activeStatuses.has(activeRun.status)
    ? (activeRun.items || []).map((item) => item.conversation_id) : []);
  useEffect(() => {
    if (correctingItemId && activeRun && activeStatuses.has(activeRun.status)
      && activeRun.items?.some((item) => item.conversation_id === correctingItemId)) {
      setCorrectingItemId("");
      message.warning(t("conversationOrganizer.deleteLocked"));
    }
  }, [activeRun, correctingItemId, t]);
  const subtitleKey = !run ? "" : run.status === "succeeded" ? "resultSubtitle"
    : activeStatuses.has(run.status) ? (canceling || run.stage === "canceling" ? "cancelingSubtitle" : "runningSubtitle")
    : run.status === "failed" ? "failedSubtitle" : run.status === "canceled" ? "canceledSubtitle" : "";
  const stageLabel = (stage?: string) => t(`conversationOrganizer.stage.${stage || "default"}`, { defaultValue: t("conversationOrganizer.stage.default") });
  const progressLabel = (value: OrganizerRun) => {
    const label = value.stage === "preparing" && value.progress.preparation_batch_total
      ? t("conversationOrganizer.preparationProgress", { current: value.progress.preparation_batch_current || 1, total: value.progress.preparation_batch_total })
      : ["organizing", "batch"].includes(value.stage) && value.progress.batch_total
        ? t("conversationOrganizer.batchProgress", { current: value.progress.batch_current, total: value.progress.batch_total })
        : stageLabel(value.stage);
    return <>{label}<span className="organizer-progress-dots" aria-hidden="true"><span>.</span><span>.</span><span>.</span></span></>;
  };
  const unassignedReasonLabel = (reason?: string, errorCode?: string) => {
    if (!reason) return "";
    const label = t(`conversationOrganizer.unassignedReason.${reason}`, { defaultValue: t("conversationOrganizer.free") });
    return reason === "summary_failed" && errorCode
      ? `${label}：${t(`conversationOrganizer.summaryError.${errorCode}`, { defaultValue: t("conversationOrganizer.summaryError.default") })}`
      : label;
  };
  const skipReasonLabel = (reason?: string) => reason ? t(`conversationOrganizer.skipReason.${reason}`, { defaultValue: reason }) : "";
  const resultItems = run?.items || [];
  const groupedResultCount = resultItems.filter((item) => item.group_id).length;
  const freeResultCount = resultItems.length - groupedResultCount;

  const refreshGroups = useCallback(async () => {
    try {
      setGroups(await listConversationGroups());
    } catch {
      // Keep history usable when this optional surface is unavailable.
    }
  }, []);

  useEffect(() => () => { ++pollGenerationRef.current; window.clearTimeout(pollRef.current); }, []);

  const refreshActiveRun = useCallback(async (runId: string, generation: number) => {
    try {
      const next = await getOrganizerRun(runId);
      if (pollGenerationRef.current !== generation) return;
      setActiveRun(next);
      setRun(current => current?.id === next.id ? next : current);
      if (activeStatuses.has(next.status)) {
        window.clearTimeout(pollRef.current);
        pollRef.current = window.setTimeout(() => void refreshActiveRun(runId, generation), 1200);
      } else {
        if (next.status === "succeeded") setHasRecentResult(true);
        void refreshGroups();
        onChangedRef.current?.();
      }
    } catch {
      if (pollGenerationRef.current !== generation) return;
      window.clearTimeout(pollRef.current);
      pollRef.current = window.setTimeout(() => void refreshActiveRun(runId, generation), 2000);
    }
  }, [refreshGroups, t]);

  useEffect(() => {
    let disposed = false;
    const restore = async () => {
      const generation = pollGenerationRef.current;
      try {
        await refreshGroups();
        const latest = await getLatestOrganizerState();
        if (disposed || generation !== pollGenerationRef.current) return;
        setFreeCount(latest.free_conversation_count ?? null);
        setHasRecentResult(Boolean(latest.latest_successful_run_id));
        setActiveRun(latest.run);
        setRun(current => current?.id === latest.run?.id ? latest.run : current);
        if (latest.run && activeStatuses.has(latest.run.status)) {
          const nextGeneration = ++pollGenerationRef.current;
          void refreshActiveRun(latest.run.id, nextGeneration);
        }
      } catch { /* Keep the last known state when disconnected. */ }
    };
    void restore();
    const timer = window.setInterval(() => { if (document.visibilityState === "visible") void restore(); }, 15000);
    window.addEventListener("focus", restore);
    window.addEventListener(CONVERSATION_GROUPS_CHANGED_EVENT, restore);
    return () => { disposed = true; window.clearInterval(timer); window.removeEventListener("focus", restore); window.removeEventListener(CONVERSATION_GROUPS_CHANGED_EVENT, restore); };
  }, [mode, refreshGroups, refreshActiveRun]);

  const namesLocked = !!activeRun && activeStatuses.has(activeRun.status);

  const showEditor = (group: ConversationGroup | "new") => {
    if (group === "new" && namesLocked) { message.info(t("conversationOrganizer.namesLocked")); return; }
    setEditingFromRun(false);
    setEditing(group);
    form.setFieldsValue(group === "new" ? { name: "", scope: "" } : {
      name: group.name, scope: group.scope || "",
    });
  };

  const saveGroup = async () => {
    if (editing === "new" && namesLocked) return;
    const values = await form.validateFields();
    const input = normalizeGroupValues(values);
    setLoading(true);
    try {
      if (editing === "new") await createConversationGroup(input);
      else if (editing) await updateConversationGroup(editing.id, editingFromRun && run ? { ...input, organizer_run_id: run.id } : input);
      setEditing(null);
      await refreshGroups();
      emitConversationGroupsChanged();
      message.success(t(editing === "new" ? "conversationOrganizer.created" : "conversationOrganizer.updated"));
    } finally { setLoading(false); }
  };

  const removeGroup = (group: ConversationGroup) => Modal.confirm({
    title: t("conversationOrganizer.removeConfirm", { name: group.name }),
    content: t("conversationOrganizer.removeHint"),
    okText: t("conversationOrganizer.removeGroup"), okButtonProps: { danger: true }, cancelText: t("common.cancel"),
    onOk: async () => {
      await deleteConversationGroup(group.id);
      await refreshGroups();
      emitConversationGroupsChanged();
      onChanged?.();
    },
  });

  const beginOrganize = async (previous = activeRun, restart = false) => {
    if (previous?.status === "failed" && (restart ? !previous.can_restart : !previous.can_retry && !previous.can_restart)) return;
    if (startingRef.current || (activeRun && activeStatuses.has(activeRun.status))) return;
    startingRef.current = true;
    const generation = ++pollGenerationRef.current;
    window.clearTimeout(pollRef.current);
    setStarting(true);
    try {
      const next = !restart && previous && (previous.status === "failed" || previous.status === "canceled") && previous.can_retry
        ? await runAction(previous.id, "retry")
        : await startOrganizerRun();
      if (generation !== pollGenerationRef.current) return;
      setActiveRun(next);
      setRun(next);
      setDrawerOpen(true);
      if (next.status === "succeeded") setHasRecentResult(true);
      onChangedRef.current?.();
      void refreshActiveRun(next.id, generation);
    } catch {
      // Refresh capabilities if the configuration changed after the drawer opened.
      if (previous) void refreshActiveRun(previous.id, generation);
    }
    finally { startingRef.current = false; setStarting(false); }
  };

  const openLatest = async () => {
    setRun(null);
    setResultLoadFailed(false);
    setResultLoading(true);
    setDrawerOpen(true);
    try {
      const latest = await getLatestSuccessfulOrganizerRun();
      setRun(latest);
      setHasRecentResult(Boolean(latest));
    } catch { setResultLoadFailed(true); } finally { setResultLoading(false); }
  };

  const closeResult = () => setDrawerOpen(false);
  const confirmResultAction = (action: "confirm" | "undo") => {
    Modal.confirm({
      title: t(`conversationOrganizer.${action}Title`),
      content: t(`conversationOrganizer.${action}Description`),
      okText: t(`conversationOrganizer.${action}Accept`),
      cancelText: t("conversationOrganizer.continueReview"),
      onOk: () => act(action),
    });
  };

  const confirmCancel = (onConfirm: () => Promise<void>) => {
    if (cancelConfirmationRef.current) return;
    cancelConfirmationRef.current = true;
    Modal.confirm({
      title: t("conversationOrganizer.cancelConfirm"),
      content: t("conversationOrganizer.cancelConfirmHint"),
      okText: t("conversationOrganizer.confirmCancel"),
      cancelText: t("conversationOrganizer.continueOrganizing"),
      onOk: onConfirm,
      afterClose: () => { cancelConfirmationRef.current = false; },
    });
  };

  const act = async (action: "cancel" | "undo" | "confirm") => {
    if (!run) return;
    if (action === "cancel") setCanceling(true);
    let next: OrganizerRun;
    try { next = await runAction(run.id, action); } finally { setCanceling(false); }
    setRun(next);
    if (action === "undo" || action === "confirm") { setDrawerOpen(false); setHasRecentResult(false); setActiveRun(null); emitConversationGroupsChanged(); }
    if (activeStatuses.has(next.status)) { setActiveRun(next); const generation = ++pollGenerationRef.current; void refreshActiveRun(next.id, generation); }
    else { await refreshGroups(); onChanged?.(); }
  };

  const correct = async (conversationId: string, groupId: string | null) => {
    if (!run) return;
    const next = await correctOrganizerItem(run.id, conversationId, { group_id: groupId });
    setRun(next);
    await refreshGroups();
    emitConversationGroupsChanged();
    onChanged?.();
  };

  const correctToNewGroup = async () => {
    if (!run || !correctingItemId || namesLocked) return;
    const values = await correctionForm.validateFields();
    const next = await correctOrganizerItem(run.id, correctingItemId, {
      new_group: normalizeGroupValues(values),
    });
    setRun(next);
    setCorrectingItemId("");
    await refreshGroups();
    emitConversationGroupsChanged();
    onChanged?.();
  };

  return <section className={`conversation-groups conversation-groups--${mode}`}>
    {mode !== "organizer" && <SidebarGroups namesLocked={namesLocked} groups={groups} searchText={searchText} currentConversationId={currentConversationId} onNew={onNewChatInGroup} onEdit={showEditor} onRemove={removeGroup} />}
    {mode !== "groups" &&
    <div className="conversation-organizer-entry">
      {activeRun && activeStatuses.has(activeRun.status) ? <Button className="conversation-organizer-active" type="text" onClick={() => { setRun(activeRun); setDrawerOpen(true); }}>{progressLabel(activeRun)}</Button>
        : hasRecentResult ? <Button className="conversation-organizer-start" type="text" icon={<CheckCircleOutlined />} onClick={() => void openLatest()}>{t("conversationOrganizer.viewResult")}<span className="organizer-unread-dot" /></Button>
        : activeRun?.status === "failed" ? <Button className="conversation-organizer-start" type="text" icon={<CloseCircleOutlined />} onClick={() => { setRun(activeRun); setDrawerOpen(true); }}>{t("conversationOrganizer.failedEntry")}</Button>
        : <Tooltip title={freeCount === 0 ? t("conversationOrganizer.noFreeConversations") : undefined}><Button className="conversation-organizer-start" type="text" loading={starting} disabled={starting || freeCount === 0} icon={<HighlightOutlined />} onClick={() => void beginOrganize()}>{t(starting ? "conversationOrganizer.starting" : "conversationOrganizer.organize")}</Button></Tooltip>}

    </div>}

    <Modal open={editing !== null} title={t(editing === "new" ? "conversationOrganizer.newGroup" : "conversationOrganizer.editGroup")} okText={t("conversationOrganizer.save")} cancelText={t("common.cancel")} confirmLoading={loading} onOk={() => void saveGroup()} onCancel={() => setEditing(null)} destroyOnClose>
      <Form form={form} layout="vertical"><GroupFields nameDisabled={namesLocked} scopeHint={t(namesLocked ? "conversationOrganizer.namesLocked" : "conversationOrganizer.scopeHint")} /></Form>
    </Modal>
    <Modal open={Boolean(correctingItemId)} title={t("conversationOrganizer.newAndMove")} okText={t("conversationOrganizer.createAndMove")} cancelText={t("common.cancel")} onOk={() => void correctToNewGroup()} onCancel={() => setCorrectingItemId("")} destroyOnClose>
      <Form form={correctionForm} layout="vertical"><GroupFields nameDisabled={namesLocked} /></Form>
    </Modal>

    <Drawer open={drawerOpen} width={480} className="conversation-organizer-drawer" title={<div className="organizer-drawer-title"><span>{run && activeStatuses.has(run.status) ? <LoadingOutlined /> : run?.status === "failed" ? <CloseCircleOutlined /> : <CheckCircleOutlined />}</span><div><strong>{run?.status === "succeeded" ? t("conversationOrganizer.done") : t("conversationOrganizer.title")}</strong><small>{subtitleKey && t(`conversationOrganizer.${subtitleKey}`)}</small></div></div>} onClose={closeResult} footer={run?.status === "succeeded" ? <div className="organizer-drawer-footer">{run.can_undo && <Button icon={<UndoOutlined />} onClick={() => confirmResultAction("undo")}>{t("conversationOrganizer.undo")}</Button>}<Button type="primary" onClick={() => confirmResultAction("confirm")}>{t("conversationOrganizer.confirmResult")}</Button></div> : null}>
      {run && <OrganizerSteps run={run} />}
      {!run ? <div className="organizer-empty">{resultLoadFailed ? <><p>{t("conversationOrganizer.loadFailed")}</p><Button onClick={() => void openLatest()}>{t("conversationOrganizer.retryLoad")}</Button></> : resultLoading ? <Spin /> : t("conversationOrganizer.noResult")}</div> : activeStatuses.has(run.status) ? <div className="organizer-progress">
        {!run.steps?.length && <h3>{progressLabel(run)}</h3>}
        <p>{t("conversationOrganizer.progressHint")}</p>
        {run.can_cancel && <Button loading={canceling} disabled={canceling} onClick={() => confirmCancel(() => act("cancel"))}>{t("conversationOrganizer.cancelRun")}</Button>}
      </div> : run.status === "failed" ? <div className="organizer-state"><CloseCircleOutlined /><h3>{t("conversationOrganizer.failed")}</h3><p>{run.error?.code ? t(`conversationOrganizer.callError.${run.error.code}`, { defaultValue: run.error.message || t("conversationOrganizer.failedHint") }) : t("conversationOrganizer.failedHint")}</p><p>{t(run.can_retry ? "conversationOrganizer.retryHint" : run.can_restart ? "conversationOrganizer.restartHint" : "conversationOrganizer.blockedHint")}</p>{run.can_retry && run.can_restart && <p>{t("conversationOrganizer.restartAlternativeHint")}</p>}{run.can_retry && <Button type="primary" loading={starting} disabled={starting} onClick={() => void beginOrganize(run)}>{t("conversationOrganizer.retry")}</Button>}{run.can_restart && <Button type={run.can_retry ? "default" : "primary"} loading={starting} disabled={starting || freeCount === 0} onClick={() => void beginOrganize(run, true)}>{t("conversationOrganizer.restart")}</Button>}</div> : run.status === "canceled" ? <div className="organizer-state"><CloseCircleOutlined /><h3>{t("conversationOrganizer.canceled")}</h3><p>{t("conversationOrganizer.canceledHint")}</p><Button loading={starting} disabled={freeCount === 0} onClick={() => void beginOrganize(run)}>{t("conversationOrganizer.organize")}</Button></div> : <>
        <div className="organizer-result-notice">{t("conversationOrganizer.resultNotice")}</div>
        <div className="organizer-result-summary" aria-label={t("conversationOrganizer.resultSummaryLabel")}>
          <div><strong>{resultItems.length}</strong><span>{t("conversationOrganizer.resultStats.included")}</span></div>
          <div><strong>{groupedResultCount}</strong><span>{t("conversationOrganizer.resultStats.assigned")}</span></div>
          <div><strong>{freeResultCount}</strong><span>{t("conversationOrganizer.resultStats.free")}</span></div>
        </div>
        {([[t('conversationOrganizer.assigned'), (run.items || []).filter((item) => item.group_id)], [t('conversationOrganizer.free'), (run.items || []).filter((item) => !item.group_id)]] as const).map(([heading, items]) => items.length > 0 && <section className="organizer-result-section" key={heading}><h4>{heading}</h4>{items.map((item) => <div className="organizer-result" key={item.conversation_id}>
          <strong>{item.title || item.conversation_id}{item.corrected ? <em className="organizer-corrected">{t("conversationOrganizer.corrected")}</em> : null}</strong><small>{skipReasonLabel(item.skip_reason) || unassignedReasonLabel(item.unassigned_reason, item.summary_error_code) || item.summary}</small>
          <div className="organizer-result-actions"><span>{t("conversationOrganizer.assignment")}</span><Select disabled={run.status !== "succeeded" || lockedConversationIds.has(item.conversation_id)} value={item.group_id || "free"} onChange={(value: string) => void correct(item.conversation_id, value === "free" ? null : value)} options={[{ value: "free", label: t("conversationOrganizer.keepFree") }, ...groups.map((group) => ({ value: group.id, label: group.name }))]} />{item.group_id && groups.some((group) => group.id === item.group_id && group.created_run_id === run.id) ? <Button type="link" disabled={run.status !== "succeeded"} onClick={() => { const group = groups.find((candidate) => candidate.id === item.group_id)!; showEditor(group); setEditingFromRun(true); }}>{t("conversationOrganizer.editGroupShort")}</Button> : null}<Button type="link" disabled={namesLocked || run.status !== "succeeded" || lockedConversationIds.has(item.conversation_id)} onClick={() => { correctionForm.resetFields(); setCorrectingItemId(item.conversation_id); }}>{t("conversationOrganizer.newAndMove")}</Button></div>
        </div>)}</section>)}
      </>}
    </Drawer>
  </section>;
}
