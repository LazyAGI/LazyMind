import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from "react";
import { ReloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { ConversationTrailRecord } from "@/modules/chat/utils/message";

const MIN_CONVERSATION_TRAIL_ITEMS = 3;
const QUESTION_PREVIEW_DELAY = 500;
const TARGET_FEEDBACK_DURATION = 1500;

interface ConversationTrailProps {
  items: ConversationTrailRecord[];
  scrollContainerRef: RefObject<HTMLDivElement>;
  messageListLength: number;
  loading?: boolean;
  error?: Error | null;
  onRetry?: () => void;
  onLocate?: (historyId: string) => Promise<boolean>;
  onNavigate?: () => void;
}

function getUserTurns(container: HTMLDivElement | null) {
  return Array.from(container?.querySelectorAll<HTMLElement>(
    '[data-chat-history-id][data-chat-role="user"]',
  ) ?? []);
}

export default function ConversationTrail({
  items,
  scrollContainerRef,
  messageListLength,
  loading = false,
  error = null,
  onRetry,
  onLocate,
  onNavigate,
}: ConversationTrailProps) {
  const { t } = useTranslation();
  const turns = useMemo(() => {
    const seen = new Set<string>();
    return items.filter((item) => {
      if (!item.history_id || seen.has(item.history_id)) return false;
      seen.add(item.history_id);
      return true;
    });
  }, [items]);
  const [activeHistoryId, setActiveHistoryId] = useState("");
  const [previewOpen, setPreviewOpen] = useState(false);
  const [previewHistoryId, setPreviewHistoryId] = useState("");
  const [questionVisible, setQuestionVisible] = useState(false);
  const [locatingHistoryId, setLocatingHistoryId] = useState("");
  const [locateFailed, setLocateFailed] = useState(false);
  const railRef = useRef<HTMLDivElement>(null);
  const hidePreviewTimerRef = useRef<number>();
  const questionTimerRef = useRef<number>();
  const targetTimerRef = useRef<number>();
  const feedbackTargetRef = useRef<HTMLElement | null>(null);
  const locateRequestRef = useRef(0);
  const pendingLocateRef = useRef(false);
  const cancelScrollRef = useRef<(() => void) | null>(null);

  const selectedHistoryId = turns.some((item) => item.history_id === activeHistoryId)
    ? activeHistoryId : turns[turns.length - 1]?.history_id;
  const previewItem = turns.find((item) => item.history_id === previewHistoryId);

  const clearFeedback = useCallback(() => {
    window.clearTimeout(targetTimerRef.current);
    feedbackTargetRef.current?.classList.remove("chat-item--trail-target");
    feedbackTargetRef.current = null;
  }, []);

  const closePreview = useCallback(() => {
    window.clearTimeout(hidePreviewTimerRef.current);
    window.clearTimeout(questionTimerRef.current);
    setPreviewOpen(false);
    setQuestionVisible(false);
    setPreviewHistoryId("");
  }, []);

  const keepPreviewOpen = () => {
    window.clearTimeout(hidePreviewTimerRef.current);
    setPreviewOpen(true);
  };

  const showPreview = (historyId: string) => {
    keepPreviewOpen();
    if (previewHistoryId === historyId) return;
    setPreviewHistoryId(historyId);
    window.clearTimeout(questionTimerRef.current);
    if (!questionVisible) {
      questionTimerRef.current = window.setTimeout(() => setQuestionVisible(true), QUESTION_PREVIEW_DELAY);
    }
  };

  const schedulePreviewHide = () => {
    window.clearTimeout(hidePreviewTimerRef.current);
    hidePreviewTimerRef.current = window.setTimeout(closePreview, 120);
  };

  const syncActiveFromScroll = useCallback(() => {
    const container = scrollContainerRef.current;
    if (!container || pendingLocateRef.current) return;
    const ids = new Set(turns.map((item) => item.history_id));
    const elements = getUserTurns(container).filter((element) => ids.has(element.dataset.chatHistoryId));
    const anchor = container.getBoundingClientRect().top + container.clientHeight * 0.32;
    let current = elements[0];
    for (const element of elements) {
      if (element.getBoundingClientRect().top <= anchor) current = element;
    }
    if (current) setActiveHistoryId(current.dataset.chatHistoryId || "");
  }, [scrollContainerRef, turns]);

  useEffect(() => {
    const frame = window.requestAnimationFrame(syncActiveFromScroll);
    return () => window.cancelAnimationFrame(frame);
  }, [messageListLength, syncActiveFromScroll]);

  useEffect(() => {
    const container = scrollContainerRef.current;
    if (!container) return;
    let frame = 0;
    const onScroll = () => {
      if (frame) return;
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        syncActiveFromScroll();
      });
    };
    container.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      container.removeEventListener("scroll", onScroll);
      window.cancelAnimationFrame(frame);
    };
  }, [scrollContainerRef, syncActiveFromScroll]);

  // Keep the current row visible without scrolling the transcript or resetting
  // a user's position while they are browsing the expanded summaries.
  useEffect(() => {
    const rail = railRef.current;
    if (!rail || previewOpen) return;
    const active = rail.querySelector<HTMLElement>('[aria-current="true"]');
    if (!active) return;
    if (active.offsetTop < rail.scrollTop) rail.scrollTop = active.offsetTop;
    else if (active.offsetTop + active.offsetHeight > rail.scrollTop + rail.clientHeight) {
      rail.scrollTop = active.offsetTop + active.offsetHeight - rail.clientHeight;
    }
  }, [selectedHistoryId, previewOpen]);

  useEffect(() => () => {
    locateRequestRef.current += 1;
    cancelScrollRef.current?.();
    window.clearTimeout(hidePreviewTimerRef.current);
    window.clearTimeout(questionTimerRef.current);
    clearFeedback();
  }, [clearFeedback]);

  const locate = async (item: ConversationTrailRecord) => {
    const historyId = item.history_id;
    const container = scrollContainerRef.current;
    if (!historyId || !container) return;
    const request = ++locateRequestRef.current;
    cancelScrollRef.current?.();
    clearFeedback();
    pendingLocateRef.current = true;
    setLocateFailed(false);
    setLocatingHistoryId(historyId);
    const findTarget = () => getUserTurns(container).find((element) => element.dataset.chatHistoryId === historyId);
    let target = findTarget();
    try {
      if (!target && onLocate) {
        const loaded = await onLocate(historyId);
        if (request !== locateRequestRef.current) return;
        if (loaded) {
          await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve()));
          if (request !== locateRequestRef.current) return;
          target = findTarget();
        }
      }
      if (!target) throw new Error("Missing turn anchor");
    } catch {
      if (request === locateRequestRef.current) {
        pendingLocateRef.current = false;
        setLocatingHistoryId("");
        setLocateFailed(true);
      }
      return;
    }

    const locatedTarget = target;
    setActiveHistoryId(historyId);
    closePreview();
    let idleTimer: number;
    const cancel = () => {
      window.clearTimeout(idleTimer);
      container.removeEventListener("scroll", onScroll);
      container.removeEventListener("scrollend", finish);
      cancelScrollRef.current = null;
    };
    const finish = () => {
      cancel();
      if (request !== locateRequestRef.current) return;
      pendingLocateRef.current = false;
      setLocatingHistoryId("");
      setActiveHistoryId(historyId);
      feedbackTargetRef.current = locatedTarget;
      locatedTarget.classList.add("chat-item--trail-target");
      targetTimerRef.current = window.setTimeout(clearFeedback, TARGET_FEEDBACK_DURATION);
    };
    const onScroll = () => {
      window.clearTimeout(idleTimer);
      idleTimer = window.setTimeout(finish, 150);
    };
    cancelScrollRef.current = cancel;
    container.addEventListener("scroll", onScroll, { passive: true });
    container.addEventListener("scrollend", finish);
    // Covers browsers without scrollend and targets that are already in view.
    onScroll();
    const reduceMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    onNavigate?.();
    locatedTarget.scrollIntoView?.({ behavior: reduceMotion ? "auto" : "smooth", block: "start" });
  };

  if (turns.length < MIN_CONVERSATION_TRAIL_ITEMS) return null;

  return (
    <aside
      className={`conversation-trail${previewOpen ? " is-previewing" : ""}`}
      aria-label={t("chat.conversationTrail")}
      onPointerLeave={schedulePreviewHide}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) schedulePreviewHide();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          closePreview();
        }
      }}
    >
      <div className="conversation-trail-content">
        <div className="conversation-trail-rail" ref={railRef} onPointerEnter={keepPreviewOpen} aria-busy={loading}>
          {turns.map((item, index) => {
            const historyId = item.history_id!;
            const isActive = historyId === selectedHistoryId;
            return (
              <button
                key={historyId}
                type="button"
                className={`conversation-trail-node${isActive ? " is-active" : ""}${historyId === previewHistoryId ? " is-hovered" : ""}`}
                aria-label={t("chat.conversationTrailItem", { index: index + 1, summary: item.summary || t("chat.conversationTrailUntitled") })}
                aria-current={isActive ? "true" : undefined}
                aria-busy={historyId === locatingHistoryId}
                onClick={() => void locate(item)}
                onPointerEnter={() => showPreview(historyId)}
                onFocus={() => showPreview(historyId)}
                onKeyDown={(event) => {
                  const offsets: Record<string, number> = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: turns.length - 1 };
                  if (!(event.key in offsets)) return;
                  event.preventDefault();
                  const nextIndex = Math.max(0, Math.min(turns.length - 1, offsets[event.key]));
                  railRef.current?.querySelectorAll<HTMLButtonElement>("button")[nextIndex]?.focus();
                }}
              >
                <span className="conversation-trail-mark" aria-hidden="true"><i /></span>
                <span className="conversation-trail-summary" aria-hidden={!previewOpen}>
                  {item.summary || t("chat.conversationTrailUntitled")}
                </span>
              </button>
            );
          })}
        </div>
        {previewOpen && questionVisible && previewItem && (
          <section key={previewItem.history_id} className="conversation-trail-question" aria-label={t("chat.conversationTrailQuestion")} onPointerEnter={keepPreviewOpen} tabIndex={0}>
            <div className="conversation-trail-question-label">{t("chat.conversationTrailQuestion")}</div>
            <div className="conversation-trail-question-text">{previewItem.question || previewItem.summary || t("chat.conversationTrailUntitled")}</div>
          </section>
        )}
        {error && (
          <button type="button" className="conversation-trail-error" onClick={onRetry} title={t("chat.conversationTrailRetry")} aria-label={t("chat.conversationTrailRetry")}>
            <ReloadOutlined />
          </button>
        )}
        {locateFailed && <div className="conversation-trail-status" role="status">{t("chat.fork.historyLoadFailed")}</div>}
      </div>
    </aside>
  );
}
