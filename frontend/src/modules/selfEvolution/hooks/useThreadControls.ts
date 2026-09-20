import { useCallback, useEffect, useRef, useState } from "react";
import { v4 as uuidv4 } from "uuid";
import { axiosInstance } from "@/components/request";
import { AGENT_API_BASE } from "../shared/constants";
import { hasLiveTerminalStatus, type ThreadObservation } from "../shared/evolutionModels";

type ExecutionAction = "pause" | "resume";
const confirmsAction = (thread: ThreadObservation | undefined, action: ExecutionAction) =>
  thread?.status_source === "live" && !thread.cleanup_pending && (
    hasLiveTerminalStatus(thread) ||
    thread.runtime_status === (action === "pause" ? "paused" : "running")
  );

export function useThreadControls(threadId?: string) {
  const [thread, setThread] = useState<ThreadObservation>();
  const [cancelState, setCancelState] = useState<"idle" | "sending" | "pending" | "failed" | "unknown">("idle");
  const [checking, setChecking] = useState(false);
  const [pendingAction, setPendingAction] = useState<ExecutionAction>();
  const [actionError, setActionError] = useState<ExecutionAction>();
  const actionCommand = useRef<{ action: ExecutionAction; id: string }>();
  const pendingActionRef = useRef<ExecutionAction>();
  const request = useRef<AbortController>();
  const scope = useRef(0);
  const command = useRef<string>();
  const inFlight = useRef(false);

  const refresh = useCallback(async () => {
    if (!threadId) return;
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const generation = scope.current;
    setChecking(true);
    try {
      const response = await axiosInstance.get(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}`, { signal: controller.signal, timeout: 10000, silentError: true } as Parameters<typeof axiosInstance.get>[1]);
      if (controller.signal.aborted || scope.current !== generation) return;
      const observed = (response.data.data || response.data).thread as ThreadObservation;
      setThread(observed);
      const action = pendingActionRef.current || actionCommand.current?.action;
      if (action && confirmsAction(observed, action)) {
        pendingActionRef.current = undefined;
        actionCommand.current = undefined;
        setPendingAction(undefined);
        setActionError(undefined);
      }
      if (hasLiveTerminalStatus(observed)) {
        setCancelState("idle");
        command.current = undefined;
      }
      return observed;
    } catch {
      if (!controller.signal.aborted && scope.current === generation) {
        setThread(previous => ({ ...previous, status_source: "cached" }));
      }
    } finally {
      if (!controller.signal.aborted && scope.current === generation) setChecking(false);
    }
  }, [threadId]);

  useEffect(() => {
    scope.current += 1;
    setThread(undefined);
    setCancelState("idle");
    command.current = undefined;
    inFlight.current = false;
    pendingActionRef.current = undefined;
    actionCommand.current = undefined;
    setPendingAction(undefined);
    setActionError(undefined);
    void refresh();
    return () => { scope.current += 1; request.current?.abort(); };
  }, [refresh]);

  useEffect(() => {
    if (!threadId || cancelState === "sending") return;
    const confirmingCancel = cancelState === "pending";
    const confirming = confirmingCancel || Boolean(pendingAction);
    const delay = confirming ? 2000 : 10000;
    let attempts = 0;
    let timer: ReturnType<typeof setTimeout>;
    let stopped = false;
    const poll = async () => {
      if (!inFlight.current) {
        const observed = await refresh();
        if (stopped) return;
        if (confirming) {
          if (confirmingCancel ? hasLiveTerminalStatus(observed) : pendingAction && confirmsAction(observed, pendingAction)) return;
          if (++attempts >= 15) {
            if (confirmingCancel) setCancelState("unknown");
            else {
              pendingActionRef.current = undefined;
              setPendingAction(undefined);
              setActionError(pendingAction);
            }
            return;
          }
        }
      }
      timer = setTimeout(poll, delay);
    };
    timer = setTimeout(poll, delay);
    return () => { stopped = true; clearTimeout(timer); };
  }, [cancelState, pendingAction, refresh, threadId]);

  const controlsAvailable = Boolean(threadId) && thread?.status_source === "live" &&
    !hasLiveTerminalStatus(thread) && !thread.cleanup_pending && cancelState === "idle" && !pendingAction;
  const canPause = Boolean(controlsAvailable && thread?.runtime_status === "running");
  const canResume = Boolean(controlsAvailable && thread?.runtime_status === "paused");
  const executeAction = async (action: ExecutionAction) => {
    if (!threadId || inFlight.current || !(action === "pause" ? canPause : canResume)) return;
    inFlight.current = true;
    if (actionCommand.current?.action !== action) actionCommand.current = { action, id: uuidv4() };
    const generation = scope.current;
    pendingActionRef.current = action;
    setPendingAction(action);
    setActionError(undefined);
    let failed = false;
    try {
      await axiosInstance.post(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/${action}`, { command_id: actionCommand.current.id }, { timeout: 15000, silentError: true } as Parameters<typeof axiosInstance.post>[2]);
    } catch { failed = true; }
    if (scope.current !== generation) return;
    const observed = await refresh();
    if (scope.current !== generation) return;
    inFlight.current = false;
    if (confirmsAction(observed, action)) return;
    if (failed) {
      pendingActionRef.current = undefined;
      setPendingAction(undefined);
      setActionError(action);
    }
  };

  const cancel = async () => {
    if (!threadId || inFlight.current || pendingActionRef.current || hasLiveTerminalStatus(thread)) return;
    inFlight.current = true;
    command.current ||= uuidv4();
    const generation = scope.current;
    setCancelState("sending");
    let failed = false;
    try {
      await axiosInstance.post(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/cancel`, { command_id: command.current }, { timeout: 15000, silentError: true } as Parameters<typeof axiosInstance.post>[2]);
    } catch { failed = true; }
    if (scope.current !== generation) return;
    // Always reconcile, including conflicts, lost responses and completion races.
    const observed = await refresh();
    if (scope.current !== generation) return;
    inFlight.current = false;
    if (hasLiveTerminalStatus(observed)) return;
    setCancelState(failed ? "failed" : "pending");
  };

  const readOnlyReason = !thread ? "loading"
    : thread.status_source !== "live" ? "unavailable"
    : thread.cleanup_pending || thread.runtime_status === "cancelling" || cancelState !== "idle" ? "cancelling"
    : pendingAction || thread.runtime_status === "pausing" ? "transition"
    : hasLiveTerminalStatus(thread) ? (["canceled", "cancelled"].includes(thread.status || "") ? "terminated" : thread.status === "failed" ? "failed" : "completed")
    : undefined;

  return { thread, cancelState, checking, refresh, cancel, pendingAction, actionError,
    canPause, canResume, pause: () => executeAction("pause"), resume: () => executeAction("resume"), readOnlyReason,
    canCancel: Boolean(threadId) && !hasLiveTerminalStatus(thread),
    readOnly: Boolean(readOnlyReason),
  };
}
