import { useEffect, useRef, useState, type RefObject } from "react";
import type { SendMessageParams } from "../../ChatInput/types";

interface Submission {
  conversationId: string;
  params: SendMessageParams;
  started: boolean;
  promise: Promise<boolean>;
  resolve: (sent: boolean) => void;
  reject: (error: unknown) => void;
}

/** Ask answers continue a card's conversation; they are never steerable drafts. */
export function useAskAnswerSubmission({ conversationId, busy, activeStreamRef, messageListRef, sendNow }: {
  conversationId: string;
  busy: boolean;
  activeStreamRef: RefObject<boolean>;
  messageListRef: RefObject<Array<{ ask_pending?: { ask_id: string }; ask_answered?: boolean; run_status?: string }>>;
  sendNow: (params: SendMessageParams) => Promise<boolean>;
}) {
  const current = useRef<Submission | null>(null);
  const [submission, setSubmission] = useState<Submission | null>(null);
  const latestAsk = messageListRef.current?.findLast((item) => item.ask_pending?.ask_id);
  const streamActive = activeStreamRef.current;

  useEffect(() => () => {
    current.current?.resolve(false);
    current.current = null;
  }, []);

  useEffect(() => {
    if (!submission || current.current !== submission || submission.started) return;
    const settle = (sent: boolean, error?: unknown) => {
      if (current.current !== submission) return;
      current.current = null;
      setSubmission(null);
      if (error !== undefined) submission.reject(error);
      else submission.resolve(sent);
    };
    if (conversationId !== submission.conversationId
      || latestAsk?.ask_pending?.ask_id !== submission.params.ask_answers_structured?.ask_id
      || latestAsk?.ask_answered
      || ["failed", "interrupted", "cancelled"].includes(latestAsk?.run_status || "")) {
      settle(false);
      return;
    }
    if (busy || activeStreamRef.current) return;
    submission.started = true;
    void (async () => {
      try { settle(await sendNow(submission.params)); }
      catch (error) { settle(false, error); }
    })();
  }, [submission, conversationId, busy, activeStreamRef, streamActive, latestAsk, sendNow]);

  const submit = (params: SendMessageParams): Promise<boolean> => {
    if (!conversationId || !params.ask_answers_structured?.ask_id) return Promise.resolve(false);
    if (current.current) {
      return current.current.conversationId === conversationId
        && current.current.params.ask_answers_structured?.ask_id === params.ask_answers_structured.ask_id
        ? current.current.promise : Promise.resolve(false);
    }
    if (!busy && !activeStreamRef.current) return sendNow(params);
    let resolve!: Submission["resolve"];
    let reject!: Submission["reject"];
    const promise = new Promise<boolean>((done, fail) => { resolve = done; reject = fail; });
    const pending = { conversationId, params, started: false, promise, resolve, reject };
    current.current = pending;
    setSubmission(pending);
    return promise;
  };

  return { submit, pending: Boolean(submission) };
}
