import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { v4 as uuid } from "uuid";
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from "@/components/auth";
import type { ForkCreateRequest, ForkPreview } from "@/api/generated/core-client";
import { CHAT_CONVERSATION_LIST_REFRESH_EVENT, getChatConversationPath } from "@/modules/chat/constants/chat";
import { createFork, previewFork } from "./api";

export type ForkOperation = { id: string; source: string; request: ForkCreateRequest; resultId?: string };
type Phase = "idle" | "previewing" | "ready" | "preview_error" | "submitting" | "unknown" | "failed";
const storagePrefix = "lazymind:fork:";
const actor = () => AgentAppsAuth.getUserInfo()?.userId || "";
let loginEpoch = 0;
let storageActor = actor();
// Keep the login boundary alive while the chat page is unmounted. An old
// request may still complete during logout/login or navigation to another app.
window.addEventListener(AUTH_USER_CHANGE_EVENT, () => {
  const next = actor();
  if (next === storageActor && next) return;
  try { if (storageActor) sessionStorage.removeItem(storagePrefix + storageActor); } catch { /* Optional storage. */ }
  storageActor = next;
  loginEpoch += 1;
});

function readOperations(): ForkOperation[] {
  if (!actor()) return [];
  try { const value = JSON.parse(sessionStorage.getItem(storagePrefix + actor()) || "[]"); return Array.isArray(value) ? value.filter((operation: ForkOperation) => !operation.resultId) : []; } catch { return []; }
}
function saveOperation(operation: ForkOperation) {
  if (!actor()) return;
  const next = readOperations().filter((item) => item.id !== operation.id);
  next.push(operation);
  try { sessionStorage.setItem(storagePrefix + actor(), JSON.stringify(next)); } catch { /* In-memory retry remains available. */ }
}
function removeOperation(id: string) {
  try { sessionStorage.setItem(storagePrefix + actor(), JSON.stringify(readOperations().filter((item) => item.id !== id))); } catch { /* Storage may be disabled. */ }
}

function requestFromPreview(preview: ForkPreview, modelId?: string): ForkCreateRequest {
  const issues = preview.config_issues.filter((issue) => issue.field !== "model");
  return {
    source_history_id: preview.source_history_id,
    expected_prefix_revision: preview.prefix_revision,
    confirmed_fields: issues.map((issue) => issue.field),
    confirmed_values: Object.fromEntries(issues.map((issue) => [issue.field, issue.suggested_value])),
    ...(modelId ? { replacement_model: { mode: "fixed", model_id: modelId } } : {}),
  };
}

export function useForkConversation(source: string) {
  const navigate = useNavigate();
  const location = useLocation();
  const navigation = useRef(location.key);
  navigation.current = location.key;
  const actorRef = useRef(actor());
  const sequence = useRef(0);
  const operationRef = useRef<ForkOperation | null>(null);
  const busy = useRef(false);
  const mounted = useRef(true);
  const [pending, setPending] = useState(false);
  const [phase, setPhase] = useState<Phase>("idle");
  const [preview, setPreview] = useState<ForkPreview>();
  const [error, setError] = useState("");
  const [target, setTarget] = useState("");
  const [recoverable, setRecoverable] = useState(readOperations);

  function setBusy(value: boolean) {
    busy.current = value;
    if (mounted.current) setPending(value);
  }

  useEffect(() => {
    mounted.current = true;
    const change = () => {
      const nextActor = actor();
      if (nextActor === actorRef.current && nextActor) return;
      try { if (actorRef.current) sessionStorage.removeItem(storagePrefix + actorRef.current); } catch { /* Optional storage. */ }
      actorRef.current = nextActor; sequence.current += 1;
      operationRef.current = null; setBusy(false); setPhase("idle"); setError(""); setRecoverable([]); setPreview(undefined);
    };
    window.addEventListener(AUTH_USER_CHANGE_EVENT, change);
    return () => { mounted.current = false; sequence.current += 1; window.removeEventListener(AUTH_USER_CHANGE_EVENT, change); };
  }, []);

  useEffect(() => { setPhase("idle"); setPreview(undefined); setError(""); sequence.current += 1; }, [source, location.key]);

  async function begin(historyId: string) {
    if (busy.current) return;
    const unresolved = (operationRef.current && !operationRef.current.resultId ? operationRef.current : null)
      || readOperations()[0];
    if (unresolved) {
      if (unresolved.source === source && unresolved.request.source_history_id === historyId) {
        await resume(unresolved);
      } else {
        setError("PENDING_FORK"); setPhase("failed");
      }
      return;
    }
    const seq = ++sequence.current; const epoch = loginEpoch; const user = actor();
    setBusy(true);
    operationRef.current = null; setTarget(historyId); setPreview(undefined); setError(""); setPhase("previewing");
    try {
      const value = await previewFork(source, historyId);
      if (!mounted.current || sequence.current !== seq || epoch !== loginEpoch || user !== actor()) return;
      setPreview(value);
      if (!value.can_fork) {
        setError(value.reason_code || "FORK_UNSUPPORTED"); setPhase("failed");
      } else if (value.config_issues.some((issue) => issue.field === "model")) {
        setPhase("ready");
      } else {
        // A Fork click accepts the suggested values for missing legacy settings.
        // Keep the server's exact preview revision and values in the frozen request.
        setBusy(false);
        await submit(requestFromPreview(value));
      }
    } catch (e) {
      if (!mounted.current || sequence.current !== seq || epoch !== loginEpoch || user !== actor()) return;
      setError(forkErrorCode(e)); setPhase("preview_error");
    } finally {
      if (epoch === loginEpoch && user === actor()) setBusy(false);
    }
  }

  async function submit(body?: ForkCreateRequest, recovery?: ForkOperation) {
    if (busy.current) return;
    const operation = recovery || operationRef.current || (body ? { id: uuid(), source, request: structuredClone(body) } : null);
    if (!operation) return;
    if (operation.resultId) { navigate(getChatConversationPath(operation.resultId)); return; }
    operationRef.current = operation; saveOperation(operation); setRecoverable(readOperations());
    setBusy(true); setPhase("submitting"); setError("");
    const nav = navigation.current; const epoch = loginEpoch; const user = actor(); const seq = sequence.current;
    try {
      const value = await createFork(operation.source, operation.id, operation.request);
      if (epoch !== loginEpoch || user !== actor()) return;
      const resultId = value.conversation.conversation_id;
      if (!resultId) throw new Error("missing result");
      operation.resultId = resultId; removeOperation(operation.id);
      window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT));
      if (mounted.current) {
        setRecoverable(readOperations());
        if (seq === sequence.current) setPhase("idle");
        if (nav === navigation.current && seq === sequence.current) { navigate(getChatConversationPath(resultId)); }
      }
    } catch (e) {
      if (epoch !== loginEpoch || user !== actor()) return;
      const status = (e as { response?: { status?: number } })?.response?.status;
      const definiteFailure = status && status >= 400 && status < 500;
      if (definiteFailure) {
        removeOperation(operation.id);
        if (operationRef.current?.id === operation.id) operationRef.current = null;
      }
      if (mounted.current && seq === sequence.current) {
        const code = forkErrorCode(e); setError(code);
        if (definiteFailure) {
          setPhase("failed");
        } else { setPhase("unknown"); }
      }
    } finally {
      if (epoch === loginEpoch && user === actor()) { setBusy(false); if (mounted.current) setRecoverable(readOperations()); }
    }
  }

  async function resume(operation: ForkOperation) {
    if (busy.current) return;
    if (operation.resultId) { navigate(getChatConversationPath(operation.resultId)); return; }
    sequence.current += 1; operationRef.current = operation; setTarget(operation.request.source_history_id); setPreview(undefined); setPhase("unknown");
    await submit(undefined, operation);
  }

  const operations = operationRef.current && !operationRef.current.resultId && !recoverable.some((operation) => operation.id === operationRef.current?.id)
    ? [...recoverable, operationRef.current] : recoverable;
  return { pending, phase, preview, error, recoverable: operations, begin, submit, resume,
    retry: () => begin(target),
    selectModel: (modelId: string) => {
      if (phase === "ready" && preview?.can_fork && modelId) return submit(requestFromPreview(preview, modelId));
    },
    dismissStatus: () => { setPhase("idle"); setError(""); setPreview(undefined); },
  };
}

export function forkErrorCode(error: unknown) {
  const code = (error as { response?: { data?: { code?: unknown } } })?.response?.data?.code;
  return typeof code === "string" ? code : "FORK_FAILED";
}
