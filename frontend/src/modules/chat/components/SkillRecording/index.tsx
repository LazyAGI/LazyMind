import { useCallback, useEffect, useRef, useState, type ChangeEvent } from "react";
import { Alert, Button, Input, Space, Tag } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { getChatConversationPath, CHAT_CONVERSATION_LIST_REFRESH_EVENT } from "../../constants/chat";
import { collectDesktopEvidence, desktopInputAvailable, prepareDesktopInput } from "./desktopEvidence";
import { captureScreen } from "./capture";
import { captureNative, nativeCaptureAvailable } from "./nativeCapture";
import { type RecordingEvidence, recordingSetup, listRecordings, submitRecording, type RecordingFrame, type SkillRecording } from "./api";
import "./style.scss";

export default function SkillRecordingPanel({ conversationId, open, onClose }: { conversationId?: string; open: boolean; onClose: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const nativeCapture = nativeCaptureAvailable();
  const collectActions = nativeCapture || desktopInputAvailable();
  const evidenceController = useRef<Promise<Awaited<ReturnType<typeof collectDesktopEvidence>>> | null>(null);
  const localEvidence = useRef<RecordingEvidence>({ events: [], limitations: [] });
  const [installed, setInstalled] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [setupLoading, setSetupLoading] = useState(false);
  const [rows, setRows] = useState<SkillRecording[]>([]);
  const [phase, setPhase] = useState<"idle" | "selecting" | "recording" | "uploading">("idle");
  const [seconds, setSeconds] = useState(0);
  const [error, setError] = useState("");
  const [notes, setNotes] = useState("");
  const [retrying, setRetrying] = useState("");
  const [localFrames, setLocalFrames] = useState<RecordingFrame[] | null>(null);
  const controller = useRef<Awaited<ReturnType<typeof captureScreen>> | null>(null);
  const generation = useRef(0);
  const validConversation = conversationId && !conversationId.startsWith("temp_") ? conversationId : undefined;
  useEffect(() => {
    if (!open && !rows.length) return;
    let active = true;
    setSetupLoading(true);
    recordingSetup().then((result) => { if (active) setInstalled(result.installed); })
      .catch(() => { if (active) setError(t("recording.loadFailed")); })
      .finally(() => { if (active) setSetupLoading(false); });
    return () => { active = false; };
  }, [open, rows.length, t]);
  const install = async () => {
    setInstalling(true); setError("");
    try { await recordingSetup(true); setInstalled(true); }
    catch (e) { setError(e instanceof Error ? e.message : t("recording.failed")); }
    finally { setInstalling(false); }
  };
  const cancelEvidence = () => {
    const pending = evidenceController.current; evidenceController.current = null;
    if (pending) void pending.then((value) => value.cancel()).catch(() => undefined);
  };
  const refresh = useCallback(async () => {
    if (!validConversation) { setRows([]); return; }
    setRows(await listRecordings({ conversation_id: validConversation }));
  }, [validConversation]);
  useEffect(() => {
    let active = true;
    const load = async () => {
      if (!validConversation) { setRows([]); return; }
      try { const next = await listRecordings({ conversation_id: validConversation }); if (active) setRows(next); }
      catch { /* Keep visible cards during transient transport failures. */ }
    };
    setRows([]); void load();
    const timer = setInterval(() => void load(), 4000);
    window.addEventListener("focus", load);
    window.addEventListener("skill-recording-updated", load);
    return () => { active = false; clearInterval(timer); window.removeEventListener("focus", load); window.removeEventListener("skill-recording-updated", load); };
  }, [validConversation]);
  useEffect(() => () => { generation.current++; controller.current?.cancel(); controller.current = null; cancelEvidence(); }, [conversationId]);
  useEffect(() => { setPhase("idle"); setError(""); setLocalFrames(null); setNotes(""); }, [conversationId]);
  useEffect(() => {
    const preventExit = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    if (phase === "recording" || phase === "uploading") window.addEventListener("beforeunload", preventExit);
    return () => window.removeEventListener("beforeunload", preventExit);
  }, [phase]);
  const upload = async (frames: RecordingFrame[], token = generation.current) => {
    setPhase("uploading"); setLocalFrames(frames); setError("");
    try {
      if (frames.length < 2) throw new Error(t("recording.tooShort"));
      const row = await submitRecording({ conversation_id: validConversation, frames, notes, evidence: localEvidence.current });
      if (generation.current !== token) return;
      setRows((old) => [...old.filter((item) => item.id !== row.id), row]);
      setLocalFrames(null); onClose();
      window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT));
      if (!validConversation) navigate(getChatConversationPath(row.conversation_id));
    } catch (e) { if (generation.current === token) setError(e instanceof Error ? e.message : t("recording.failed")); }
    finally { if (generation.current === token) setPhase("idle"); }
  };
  const start = async () => {
    if (phase !== "idle") return;
    localEvidence.current = { events: [], limitations: collectActions ? [] : ["system_input_unavailable_in_web_browser"] };
    setError(""); setLocalFrames(null); setPhase("selecting"); setSeconds(0);
    const token = ++generation.current;
    let screenEnded = false;
    try {
      if (nativeCapture) {
        const next = await captureNative((result) => {
          controller.current = null;
          if (token !== generation.current) return;
          localEvidence.current = result.evidence;
          void upload(result.frames, token);
        }, setSeconds, (failure) => {
          if (token !== generation.current) return;
          controller.current = null; setError(t("recording.captureFailed")); setPhase("idle");
          console.warn("Recording helper stopped:", failure.message);
        });
        if (token !== generation.current) { next.cancel(); return; }
        controller.current = next; setPhase("recording"); return;
      }
      await prepareDesktopInput();
      if (token !== generation.current) return;
      const next = await captureScreen((frames) => {
        screenEnded = true;
        controller.current = null;
        if (token !== generation.current) return;
        setPhase("uploading");
        const pending = evidenceController.current;
        void (async () => {
          try {
            if (pending) localEvidence.current = await (await pending).finish();
            if (token === generation.current) { evidenceController.current = null; await upload(frames, token); }
          } catch { if (token === generation.current) { setError(t("recording.eventsUnavailable")); setPhase("idle"); } }
        })();
      }, setSeconds);
      if (token !== generation.current) { next.cancel(); return; }
      controller.current = next;
      if (collectActions && !screenEnded) {
        const pending = collectDesktopEvidence(next.startedAt);
        evidenceController.current = pending;
        try { await pending; }
        catch { next.cancel(); controller.current = null; throw new Error("events-unavailable"); }
        if (token !== generation.current) { void pending.then((value) => value.cancel()); next.cancel(); return; }
      }
      if (!screenEnded) setPhase("recording");
    } catch (e) {
      if (token !== generation.current) return;
      const name = e && typeof e === "object" && "name" in e ? String(e.name) : "";
      if (e instanceof Error && e.message.includes("recording-cancelled")) { setPhase("idle"); return; }
      if (e instanceof Error && e.message.includes("recording-helper-input-permission")) { setError(t("recording.helperInputPermission")); setPhase("idle"); return; }
      if (e instanceof Error && e.message.includes("recording-helper-screen-permission")) { setError(t("recording.helperScreenPermission")); setPhase("idle"); return; }
      setError(t(e instanceof Error && e.message.includes("recording-input-permission") ? "recording.inputPermission" : e instanceof Error && e.message === "events-unavailable" ? "recording.eventsUnavailable" : name === "NotAllowedError" || name === "NotReadableError" ? "recording.permission" : name === "AbortError" ? "recording.captureAborted" : name === "InvalidStateError" ? "recording.activationRequired" : e instanceof Error && e.message === "unsupported" ? "recording.unsupported" : "recording.captureFailed"));
      setPhase("idle");
    }
  };
  const cancel = () => { if (nativeCapture) void window.lazymindDesktop?.recordingNativeCancel?.(); cancelEvidence(); generation.current++; controller.current?.cancel(); controller.current = null; setPhase("idle"); setLocalFrames(null); setError(""); onClose(); };
  const retry = async (row: SkillRecording) => {
    setRetrying(row.id); setError("");
    try { await submitRecording({ id: row.id, notes }); await refresh(); }
    catch (e) { setError(e instanceof Error ? e.message : t("recording.failed")); }
    finally { setRetrying(""); }
  };
  if (!open && !rows.length && !error && phase === "idle") return null;
  return <section className="skill-recording-panel" aria-label={t("recording.title")}>
    {error && <Alert type="error" showIcon message={error} closable onClose={() => setError("")} />}
    {(open || phase !== "idle" || localFrames || error) && <article className="skill-recording-card"><h3>{t(installed ? "recording.title" : "recording.installTitle")}</h3>
      <p>{t("recording.privacy")}</p>
      <p>{t("recording.explanation")}</p>
      <p>{t(collectActions ? "recording.desktopActions" : "recording.webActions")}</p>
      {nativeCapture && (error === t("recording.helperInputPermission") || error === t("recording.helperScreenPermission")) && <Button type="link" onClick={() => void window.lazymindDesktop?.recordingNativeSettings?.().catch(() => setError(t("recording.captureFailed")))}>{t("recording.openInputSettings")}</Button>}
      {collectActions && error === t("recording.inputPermission") && <Button type="link" onClick={() => void window.lazymindDesktop?.recordingInputSettings?.()}>{t("recording.openInputSettings")}</Button>}
      <Input.TextArea aria-label={t("recording.notes")} placeholder={t("recording.notes")} value={notes} onChange={(e: ChangeEvent<HTMLTextAreaElement>) => setNotes(e.target.value)} maxLength={8000} disabled={phase !== "idle"} />
      <Space wrap style={{ marginTop: 12 }}>
        {!installed ? <Button type="primary" loading={installing || setupLoading} onClick={() => void install()}>{t("recording.install")}</Button> : phase === "recording" ? <><Tag color="red" role="status">{t("recording.live", { seconds })}</Tag><Button type="primary" onClick={() => controller.current?.stop()}>{t("recording.stop")}</Button></>
          : <Button type="primary" loading={phase !== "idle"} onClick={() => void start()}>{t("recording.start")}</Button>}
        {localFrames && phase === "idle" && <Button onClick={() => void upload(localFrames)}>{t("recording.retryUpload")}</Button>}
        <Button disabled={phase === "uploading"} onClick={cancel}>{t("common.cancel")}</Button>
      </Space>
    </article>}
    {rows.map((row) => <article className="skill-recording-card" key={row.id}><header><h3>{row.name || t("recording.title")}</h3><Tag color={row.status === "pending" ? "orange" : undefined}>{t(`recording.status.${row.status}`)}</Tag></header>
      <p>{row.error || row.description || t("recording.analyzing")}</p>
      {(row.status === "pending" || row.status === "kept") && <Button onClick={() => navigate(`/memory-management/skills/${encodeURIComponent(row.skill_id)}`)}>{t(row.status === "pending" ? "recording.review" : "recording.view")}</Button>}
      {(row.status === "failed" || row.status === "needs_input") && <>
        <Input.TextArea aria-label={t("recording.notes")} placeholder={t("recording.notes")} value={notes} onChange={(e: ChangeEvent<HTMLTextAreaElement>) => setNotes(e.target.value)} maxLength={8000} />
        <Space style={{ marginTop: 8 }}><Button loading={retrying === row.id} disabled={Boolean(retrying)} onClick={() => void retry(row)}>{t("recording.retry")}</Button><Button disabled={phase !== "idle"} onClick={() => void start()}>{t("recording.rerecord")}</Button></Space>
      </>}
    </article>)}
  </section>;
}
