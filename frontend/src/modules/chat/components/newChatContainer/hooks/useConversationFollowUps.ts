import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { message } from "antd";
import { useTranslation } from "react-i18next";

import type { SendMessageParams } from "../../ChatInput/types";
import {
  ConversationFollowUpApi,
  type ConversationFollowUp,
} from "../../../utils/request";

function serializableMessage(params: SendMessageParams): Record<string, unknown> {
  const {
    fileListRef: _fileListRef,
    files: _files,
    onMailSubmissionRefused: _onMailSubmissionRefused,
    ...content
  } = params;
  return JSON.parse(JSON.stringify(content)) as Record<string, unknown>;
}

function followUpFingerprint(content: Record<string, unknown>): string {
  const stableContent = { ...content };
  // A retry of the same draft may be rebuilt with a new client timestamp. It
  // must still reuse the original idempotency key after an ambiguous failure.
  delete stableContent.create_time;
  return JSON.stringify(stableContent);
}

function queuedMessageParams(item: ConversationFollowUp): SendMessageParams {
  return {
    ...(item.content as unknown as SendMessageParams),
    text: item.text,
    clearInput: false,
  };
}

export function useConversationFollowUps({
  conversationId,
  isStreaming,
  sendNow,
}: {
  conversationId: string;
  isStreaming: boolean;
  sendNow: (params: SendMessageParams) => Promise<boolean>;
}) {
  const { t } = useTranslation();
  const [items, setItems] = useState<ConversationFollowUp[]>([]);
  const [activeRunId, setActiveRunId] = useState("");
  const [supportsSteering, setSupportsSteering] = useState(false);
  const [stateConversationId, setStateConversationId] = useState(conversationId);
  const currentConversationIdRef = useRef(conversationId);
  const latestRefreshRef = useRef(0);
  const dispatchingConversationsRef = useRef(new Set<string>());
  const enqueueAttemptsRef = useRef(new Map<string, { fingerprint: string; idempotencyKey: string }>());
  const enqueueRequestsRef = useRef(new Map<string, { fingerprint: string; promise: Promise<boolean> }>());
  const mountedRef = useRef(true);
  currentConversationIdRef.current = conversationId;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    // Do not render or act on the previous conversation while its final poll is
    // still in flight. The response guard in refresh also prevents that poll
    // from overwriting the new conversation after it resolves.
    latestRefreshRef.current += 1;
    setStateConversationId(conversationId);
    setItems([]);
    setActiveRunId("");
    setSupportsSteering(false);
  }, [conversationId]);

  const refresh = useCallback(async () => {
    const requestedConversationId = conversationId;
    if (currentConversationIdRef.current !== requestedConversationId) return;
    if (!requestedConversationId || requestedConversationId.startsWith("temp_")) {
      setItems([]);
      setActiveRunId("");
      setSupportsSteering(false);
      return;
    }
    const refreshID = ++latestRefreshRef.current;
    const result = await ConversationFollowUpApi.list(requestedConversationId);
    if (
      !mountedRef.current
      || currentConversationIdRef.current !== requestedConversationId
      || latestRefreshRef.current !== refreshID
    ) return;
    setStateConversationId(requestedConversationId);
    setItems(result.items || []);
    setActiveRunId(result.active_run_id || "");
    setSupportsSteering(Boolean(result.supports_steering));
  }, [conversationId]);

  useEffect(() => {
    void refresh().catch(() => undefined);
    if (!conversationId || conversationId.startsWith("temp_")) return;
    // Keep idle clients in sync too: another tab may reorder the queue or
    // atomically claim its head after the active run finishes.
    const timer = window.setInterval(
      () => void refresh().catch(() => undefined),
      isStreaming ? 1500 : 5000,
    );
    return () => window.clearInterval(timer);
  }, [conversationId, isStreaming, refresh]);

  const enqueue = useCallback(async (
    params: SendMessageParams,
    conversationIdOverride?: string,
  ) => {
    // A newly-created conversation can already have an active stream before
    // the route prop has rerendered with its server id. Accept that resolved id
    // from the caller so an immediate second send is queued instead of being
    // mistaken for another ordinary turn.
    const requestedConversationId = conversationIdOverride || conversationId;
    if (!requestedConversationId || requestedConversationId.startsWith("temp_")) return false;
    const content = serializableMessage(params);
    const fingerprint = followUpFingerprint(content);
    const inFlight = enqueueRequestsRef.current.get(requestedConversationId);
    if (inFlight) {
      // Coalesce a repeated click/Enter for the same draft. If the user has
      // already changed the draft, keep it in the composer until this request
      // settles instead of silently replacing the first queued message.
      return inFlight.fingerprint === fingerprint ? inFlight.promise : false;
    }

    let attempt = enqueueAttemptsRef.current.get(requestedConversationId);
    if (!attempt || attempt.fingerprint !== fingerprint) {
      attempt = { fingerprint, idempotencyKey: crypto.randomUUID() };
      enqueueAttemptsRef.current.set(requestedConversationId, attempt);
    }
    const currentAttempt = attempt;
    const request = (async () => {
      try {
        const item = await ConversationFollowUpApi.create(requestedConversationId, {
          text: params.text,
          content,
          idempotency_key: currentAttempt.idempotencyKey,
        });
        if (enqueueAttemptsRef.current.get(requestedConversationId) === currentAttempt) {
          enqueueAttemptsRef.current.delete(requestedConversationId);
        }
        if (currentConversationIdRef.current === requestedConversationId) {
          setStateConversationId(requestedConversationId);
          setItems((current) => [...current.filter((entry) => entry.id !== item.id), item]
            .sort((left, right) => left.sort_order - right.sort_order));
        }
        return true;
      } catch {
        message.error(t("chat.followUpQueueFailed"));
        return false;
      }
    })();
    enqueueRequestsRef.current.set(requestedConversationId, { fingerprint, promise: request });
    try {
      return await request;
    } finally {
      if (enqueueRequestsRef.current.get(requestedConversationId)?.promise === request) {
        enqueueRequestsRef.current.delete(requestedConversationId);
      }
    }
  }, [conversationId, t]);

  const steer = useCallback(async (item: ConversationFollowUp) => {
    const requestedConversationId = conversationId;
    if (
      !requestedConversationId
      || item.conversation_id !== requestedConversationId
      || item.status === "steering"
      || item.status === "accepted"
    ) return;
    setItems((current) => current.map((entry) => entry.id === item.id
      ? { ...entry, mode: "steer", status: "steering" }
      : entry));
    try {
      const result = await ConversationFollowUpApi.steer(requestedConversationId, item.id, activeRunId);
      if (currentConversationIdRef.current === requestedConversationId) {
        setItems((current) => current.map((entry) => entry.id === item.id ? result.item : entry));
      }
    } catch {
      if (currentConversationIdRef.current === requestedConversationId) {
        await refresh().catch(() => undefined);
        message.error(t("chat.followUpSteerFailed"));
      }
    }
  }, [activeRunId, conversationId, refresh, t]);

  const retry = useCallback(async (item: ConversationFollowUp) => {
    const requestedConversationId = conversationId;
    if (!requestedConversationId || item.conversation_id !== requestedConversationId || item.status !== "failed") return;
    try {
      const pending = await ConversationFollowUpApi.retry(requestedConversationId, item.id);
      if (currentConversationIdRef.current !== requestedConversationId) return;
      setItems((current) => current.map((entry) => entry.id === item.id ? pending : entry));
      if (item.mode === "steer" && isStreaming) {
        await steer(pending);
      }
    } catch {
      message.error(t("chat.followUpRetryFailed"));
    }
  }, [conversationId, isStreaming, steer, t]);

  const remove = useCallback(async (item: ConversationFollowUp) => {
    const requestedConversationId = conversationId;
    if (!requestedConversationId || item.conversation_id !== requestedConversationId) return;
    try {
      await ConversationFollowUpApi.remove(requestedConversationId, item.id);
      if (currentConversationIdRef.current === requestedConversationId) {
        setItems((current) => current.filter((entry) => entry.id !== item.id));
      }
    } catch {
      if (currentConversationIdRef.current === requestedConversationId) {
        message.error(t("chat.followUpDeleteFailed"));
      }
    }
  }, [conversationId, t]);

  const update = useCallback(async (item: ConversationFollowUp, text: string) => {
    const requestedConversationId = conversationId;
    if (!requestedConversationId || item.conversation_id !== requestedConversationId) return;
    const content = { ...item.content, text };
    try {
      const updated = await ConversationFollowUpApi.update(requestedConversationId, item.id, { text, content });
      if (currentConversationIdRef.current === requestedConversationId) {
        setItems((current) => current.map((entry) => entry.id === item.id ? updated : entry));
      }
    } catch {
      if (currentConversationIdRef.current === requestedConversationId) {
        message.error(t("chat.followUpEditFailed"));
      }
    }
  }, [conversationId, t]);

  const visibleItems = stateConversationId === conversationId ? items : [];

  const reorder = useCallback(async (orderedIds: string[]) => {
    const requestedConversationId = conversationId;
    const pending = visibleItems.filter((item) => item.status === "pending");
    if (!requestedConversationId || pending.length !== orderedIds.length) return;
    const previous = visibleItems;
    const byID = new Map(pending.map((item) => [item.id, item]));
    const reordered = orderedIds.map((id, index) => ({
      ...byID.get(id)!, sort_order: (index + 1) * 1024,
    }));
    const stationary = visibleItems.filter((item) => item.status !== "pending");
    setItems([...reordered, ...stationary]);
    try {
      await ConversationFollowUpApi.reorder(requestedConversationId, orderedIds);
      if (currentConversationIdRef.current !== requestedConversationId) return;
      await refresh();
    } catch {
      if (currentConversationIdRef.current === requestedConversationId) {
        setItems(previous);
        message.warning(t("chat.followUpReorderConflict"));
        await refresh().catch(() => undefined);
      }
    }
  }, [conversationId, refresh, t, visibleItems]);

  useEffect(() => {
    const requestedConversationId = conversationId;
    if (
      isStreaming
      || !requestedConversationId
      || dispatchingConversationsRef.current.has(requestedConversationId)
    ) return;
    if (!visibleItems.some((item) => item.mode === "queued" && item.status === "pending")) return;
    dispatchingConversationsRef.current.add(requestedConversationId);
    void (async () => {
      let claimed: ConversationFollowUp | null = null;
      let shouldRefresh = false;
      try {
        claimed = (await ConversationFollowUpApi.claimNext(requestedConversationId)).item;
        if (!claimed) return;
        shouldRefresh = true;
        if (currentConversationIdRef.current !== requestedConversationId) {
          await ConversationFollowUpApi.complete(
            requestedConversationId,
            claimed.id,
            false,
            "conversation changed before dispatch",
            true,
          );
          return;
        }
        setItems((current) => current.map((entry) => entry.id === claimed?.id ? claimed : entry));
        const sent = await sendNow(queuedMessageParams(claimed));
        await ConversationFollowUpApi.complete(requestedConversationId, claimed.id, sent);
        if (!sent) message.error(t("chat.followUpDispatchFailed"));
      } catch (error) {
        if (claimed) {
          await ConversationFollowUpApi.complete(requestedConversationId, claimed.id, false, error).catch(() => undefined);
        }
        if (currentConversationIdRef.current === requestedConversationId) {
          message.error(t("chat.followUpDispatchFailed"));
        }
      } finally {
        dispatchingConversationsRef.current.delete(requestedConversationId);
        // A null claim means Core still sees an active run. Wait for the normal
        // poll instead of creating a tight claim -> refresh -> claim loop.
        if (shouldRefresh && currentConversationIdRef.current === requestedConversationId) {
          await refresh().catch(() => undefined);
        }
      }
    })();
  }, [conversationId, isStreaming, refresh, sendNow, t, visibleItems]);

  const visibleActiveRunId = stateConversationId === conversationId ? activeRunId : "";
  const visibleSupportsSteering = stateConversationId === conversationId && supportsSteering;

  return useMemo(() => ({
    items: visibleItems,
    activeRunId: visibleActiveRunId,
    supportsSteering: visibleSupportsSteering,
    enqueue,
    steer,
    retry,
    remove,
    update,
    reorder,
    refresh,
  }), [enqueue, refresh, remove, reorder, retry, steer, update, visibleActiveRunId, visibleItems, visibleSupportsSteering]);
}
