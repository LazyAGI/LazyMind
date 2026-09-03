import { useCallback, useEffect, useRef, useState } from "react";
import {
  getLatestPreferenceOrganizer,
  submitPreferenceOrganizer,
  type PreferenceOrganizerTask,
} from "../currentMemoryApi";

const active = (task: PreferenceOrganizerTask | null) =>
  task?.status === "pending" || task?.status === "running";

/** Core owns task identity and lifetime; this hook only observes a visible page. */
export function usePreferenceOrganizer(
  onFinished: (task: PreferenceOrganizerTask) => void,
) {
  const [task, setTask] = useState<PreferenceOrganizerTask | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const previous = useRef<PreferenceOrganizerTask | null>(null);
  const initialized = useRef(false);
  const mounted = useRef(false);
  const submitInFlight = useRef(false);
  const requestSequence = useRef(0);
  const finished = useRef(onFinished);
  finished.current = onFinished;

  const accept = useCallback((next: PreferenceOrganizerTask | null, submitted = false) => {
    const old = previous.current;
    previous.current = next;
    setTask(next);
    setError(null);
    if (next && !active(next) && (submitted || (initialized.current &&
      (old?.task_id !== next.task_id || active(old))))) {
      finished.current(next);
    }
    initialized.current = true;
  }, []);

  const refresh = useCallback(async (signal?: AbortSignal) => {
    const sequence = ++requestSequence.current;
    try {
      const next = await getLatestPreferenceOrganizer(signal);
      if (mounted.current && !signal?.aborted && sequence === requestSequence.current) accept(next);
    } catch (failure) {
      if (mounted.current && !signal?.aborted && sequence === requestSequence.current) setError(failure);
    }
  }, [accept]);

  useEffect(() => {
    mounted.current = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    const stop = () => { clearTimeout(timer); controller?.abort(); };
    const poll = async () => {
      if (document.visibilityState === "hidden") return;
      const currentController = new AbortController();
      controller = currentController;
      await refresh(currentController.signal);
      if (mounted.current && !currentController.signal.aborted && !document.hidden) {
        timer = setTimeout(() => void poll(), 2000);
      }
    };
    const visibility = () => { stop(); if (document.visibilityState !== "hidden") void poll(); };
    document.addEventListener("visibilitychange", visibility);
    void poll();
    return () => { mounted.current = false; stop(); document.removeEventListener("visibilitychange", visibility); };
  }, [refresh]);

  const submit = useCallback(async () => {
    if (submitInFlight.current) return;
    submitInFlight.current = true;
    setSubmitting(true);
    ++requestSequence.current;
    try {
      const next = await submitPreferenceOrganizer();
      // Invalidate a poll started while submitting, too.
      ++requestSequence.current;
      if (mounted.current) accept(next, true);
    } finally {
      submitInFlight.current = false;
      if (mounted.current) setSubmitting(false);
    }
  }, [accept]);

  return { task, submitting, error, refresh, submit, running: task?.status === "running", active: active(task) };
}
