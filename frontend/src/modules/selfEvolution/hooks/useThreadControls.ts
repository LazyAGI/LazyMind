import { useCallback, useEffect, useRef, useState } from "react";
import { v4 as uuidv4 } from "uuid";
import { axiosInstance } from "@/components/request";
import { AGENT_API_BASE } from "../shared/constants";
import { hasLiveTerminalStatus, type ThreadObservation } from "../shared/evolutionModels";

export function useThreadControls(threadId?: string) {
  const [thread, setThread] = useState<ThreadObservation>();
  const [cancelState, setCancelState] = useState<"idle" | "sending" | "pending" | "failed" | "unknown">("idle");
  const [checking, setChecking] = useState(false);
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
    void refresh();
    const timer = setInterval(() => { if (!inFlight.current) void refresh(); }, 10000);
    return () => { clearInterval(timer); scope.current += 1; request.current?.abort(); };
  }, [refresh]);

  useEffect(() => {
    if (cancelState !== "pending") return;
    let attempts = 0;
    let timer: ReturnType<typeof setTimeout>;
    let stopped = false;
    const poll = async () => {
      const observed = await refresh();
      if (stopped || hasLiveTerminalStatus(observed)) return;
      if (++attempts >= 15) { setCancelState("unknown"); return; }
      timer = setTimeout(poll, 2000);
    };
    timer = setTimeout(poll, 2000);
    return () => { stopped = true; clearTimeout(timer); };
  }, [cancelState, refresh]);

  const cancel = async () => {
    if (!threadId || inFlight.current || hasLiveTerminalStatus(thread)) return;
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

  return { thread, cancelState, checking, refresh, cancel,
    canCancel: Boolean(threadId) && !hasLiveTerminalStatus(thread),
    readOnly: !thread || thread.status_source !== "live" || hasLiveTerminalStatus(thread) || thread.cleanup_pending || thread.runtime_status === "cancelling" || cancelState !== "idle",
  };
}
