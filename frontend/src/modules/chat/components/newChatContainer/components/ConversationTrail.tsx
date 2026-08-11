import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type RefObject,
} from "react";
import { MenuFoldOutlined, MenuUnfoldOutlined, ReloadOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { ConversationTrailRecord } from "@/modules/chat/utils/message";

const TRAIL_COLORS = ["#5f6670", "#858b93", "#a8adb4", "#c5c9ce", "#d9dce0"];
const TRAIL_MAX_WIDTH = 28;
const TRAIL_MIN_WIDTH = 4;
const TRAIL_WIDTH_DECAY = 4;
const MIN_CONVERSATION_TRAIL_ITEMS = 3;

interface ConversationTrailProps {
  items: ConversationTrailRecord[];
  scrollContainerRef: RefObject<HTMLDivElement>;
  messageListLength: number;
  loading?: boolean;
  error?: Error | null;
  onRetry?: () => void;
  onLocate?: (historyId: string) => void;
}

function distanceColor(activeIndex: number, itemIndex: number) {
  return TRAIL_COLORS[Math.min(Math.abs(activeIndex - itemIndex), TRAIL_COLORS.length - 1)];
}

function trailWidth(activeIndex: number, itemIndex: number) {
  const distance = Math.abs(activeIndex - itemIndex);
  return Math.max(TRAIL_MIN_WIDTH, TRAIL_MAX_WIDTH - distance * TRAIL_WIDTH_DECAY);
}

function getTargetElement(
  container: HTMLDivElement | null,
  historyId: string,
) {
  if (!container) {
    return null;
  }
  return Array.from(
    container.querySelectorAll<HTMLElement>("[data-chat-history-id]"),
  ).find((element) => element.dataset.chatHistoryId === historyId);
}

export default function ConversationTrail({
  items,
  scrollContainerRef,
  messageListLength,
  loading = false,
  error = null,
  onRetry,
  onLocate,
}: ConversationTrailProps) {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState(false);
  const [activeHistoryId, setActiveHistoryId] = useState("");
  const [previewHistoryId, setPreviewHistoryId] = useState("");
  const hidePreviewTimerRef = useRef<number | null>(null);
  const targetTimerRef = useRef<number | null>(null);

  const activeIndex = useMemo(() => {
    const index = items.findIndex((item) => item.history_id === activeHistoryId);
    return index >= 0 ? index : Math.max(items.length - 1, 0);
  }, [activeHistoryId, items]);

  const rippleCenterIndex = useMemo(() => {
    const previewIndex = items.findIndex(
      (item) => item.history_id === previewHistoryId,
    );
    return previewIndex >= 0 ? previewIndex : activeIndex;
  }, [activeIndex, items, previewHistoryId]);

  const syncActiveFromScroll = useCallback(() => {
    const container = scrollContainerRef.current;
    if (!container || items.length === 0) {
      return;
    }
    const anchor = container.getBoundingClientRect().top + container.clientHeight * 0.32;
    let nextIndex = 0;
    const elements = Array.from(
      container.querySelectorAll<HTMLElement>("[data-chat-history-id]"),
    );
    items.forEach((item, index) => {
      const target = elements.find(
        (element) => element.dataset.chatHistoryId === item.history_id,
      );
      if (target && target.getBoundingClientRect().top <= anchor) {
        nextIndex = index;
      }
    });
    const nextHistoryId = items[nextIndex]?.history_id || "";
    setActiveHistoryId((current) =>
      current === nextHistoryId ? current : nextHistoryId,
    );
  }, [items, scrollContainerRef]);

  useEffect(() => {
    if (items.length === 0) {
      setActiveHistoryId("");
      return;
    }
    if (!items.some((item) => item.history_id === activeHistoryId)) {
      setActiveHistoryId(items[items.length - 1]?.history_id || "");
    }
    const frame = window.requestAnimationFrame(syncActiveFromScroll);
    return () => window.cancelAnimationFrame(frame);
  }, [activeHistoryId, items, messageListLength, syncActiveFromScroll]);

  useEffect(() => {
    const container = scrollContainerRef.current;
    if (!container) {
      return;
    }
    let frame = 0;
    const onScroll = () => {
      if (previewHistoryId) {
        setPreviewHistoryId("");
      }
      if (frame) {
        return;
      }
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        syncActiveFromScroll();
      });
    };
    container.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      container.removeEventListener("scroll", onScroll);
      if (frame) {
        window.cancelAnimationFrame(frame);
      }
    };
  }, [previewHistoryId, scrollContainerRef, syncActiveFromScroll]);

  useEffect(() => {
    return () => {
      if (hidePreviewTimerRef.current) {
        window.clearTimeout(hidePreviewTimerRef.current);
      }
      if (targetTimerRef.current) {
        window.clearTimeout(targetTimerRef.current);
      }
    };
  }, []);

  const showPreview = (historyId: string) => {
    if (hidePreviewTimerRef.current) {
      window.clearTimeout(hidePreviewTimerRef.current);
      hidePreviewTimerRef.current = null;
    }
    setPreviewHistoryId(historyId);
  };

  const schedulePreviewHide = () => {
    if (hidePreviewTimerRef.current) {
      window.clearTimeout(hidePreviewTimerRef.current);
    }
    hidePreviewTimerRef.current = window.setTimeout(() => {
      setPreviewHistoryId("");
      hidePreviewTimerRef.current = null;
    }, 100);
  };

  const locate = (item: ConversationTrailRecord) => {
    const historyId = item.history_id || "";
    if (!historyId) {
      return;
    }
    const target = getTargetElement(scrollContainerRef.current, historyId);
    if (target) {
      target.scrollIntoView?.({ behavior: "smooth", block: "start" });
      target.classList.remove("chat-item--trail-target");
      void target.offsetWidth;
      target.classList.add("chat-item--trail-target");
      if (targetTimerRef.current) {
        window.clearTimeout(targetTimerRef.current);
      }
      targetTimerRef.current = window.setTimeout(() => {
        target.classList.remove("chat-item--trail-target");
        targetTimerRef.current = null;
      }, 1650);
    }
    setActiveHistoryId(historyId);
    onLocate?.(historyId);
  };

  if (items.length < MIN_CONVERSATION_TRAIL_ITEMS) {
    return null;
  }

  return (
    <aside
      className={`conversation-trail${collapsed ? " is-collapsed" : ""}${previewHistoryId ? " is-previewing" : ""}`}
      aria-label={t("chat.conversationTrail")}
    >
      {collapsed ? (
        <button
          type="button"
          className="conversation-trail-toggle conversation-trail-toggle--collapsed"
          aria-label={t("chat.conversationTrailExpand")}
          title={t("chat.conversationTrailExpand")}
          onClick={() => setCollapsed(false)}
        >
          <MenuUnfoldOutlined />
        </button>
      ) : (
        <>
          <div className="conversation-trail-rail">
            {loading ? (
              <span
                className="conversation-trail-loading"
                role="status"
                aria-label={t("chat.conversationTrailLoading")}
              />
            ) : (
              items.map((item, index) => {
                const historyId = item.history_id || `trail-${index}`;
                const isActive = historyId === activeHistoryId;
                return (
                  <button
                    key={historyId}
                    type="button"
                    className={`conversation-trail-node${isActive ? " is-active" : ""}${historyId === previewHistoryId ? " is-hovered" : ""}`}
                    style={{
                      "--trail-width": `${trailWidth(rippleCenterIndex, index)}px`,
                      "--trail-color": distanceColor(rippleCenterIndex, index),
                      "--trail-depth": item.depth ?? 0,
                    } as CSSProperties}
                    aria-label={t("chat.conversationTrailItem", {
                      index: index + 1,
                      summary: item.summary || t("chat.conversationTrailUntitled"),
                    })}
                    aria-current={isActive ? "true" : undefined}
                    onClick={() => locate(item)}
                    onPointerEnter={() => showPreview(historyId)}
                    onPointerLeave={schedulePreviewHide}
                    onFocus={() => showPreview(historyId)}
                    onBlur={schedulePreviewHide}
                    onKeyDown={(event) => {
                      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                        event.preventDefault();
                        const nextIndex = Math.max(
                          0,
                          Math.min(
                            items.length - 1,
                            index + (event.key === "ArrowDown" ? 1 : -1),
                          ),
                        );
                        const next = items[nextIndex];
                        if (next) {
                          showPreview(next.history_id || "");
                          locate(next);
                        }
                      }
                    }}
                  >
                    <span aria-hidden="true" />
                  </button>
                );
              })
            )}
          </div>
          {error ? (
            <button
              type="button"
              className="conversation-trail-error"
              onClick={onRetry}
              title={t("chat.conversationTrailRetry")}
              aria-label={t("chat.conversationTrailRetry")}
            >
              <ReloadOutlined />
            </button>
          ) : null}
          <button
            type="button"
            className="conversation-trail-toggle"
            aria-label={t("chat.conversationTrailCollapse")}
            title={t("chat.conversationTrailCollapse")}
            onClick={() => setCollapsed(true)}
          >
            <MenuFoldOutlined />
          </button>
          {previewHistoryId ? (
            <div
              className="conversation-trail-popover"
              onPointerEnter={() => showPreview(previewHistoryId)}
              onPointerLeave={schedulePreviewHide}
            >
              {items.map((item, index) => {
                const historyId = item.history_id || `trail-${index}`;
                const isActive = historyId === activeHistoryId;
                const isHovered = historyId === previewHistoryId;
                return (
                  <button
                    key={historyId}
                    type="button"
                    className={`conversation-trail-popover-item${isActive ? " is-active" : ""}${isHovered ? " is-hovered" : ""}`}
                    style={{ "--trail-depth": item.depth ?? 0 } as CSSProperties}
                    aria-current={isActive ? "true" : undefined}
                    title={item.question || item.summary || ""}
                    onClick={() => {
                      locate(item);
                      setPreviewHistoryId("");
                    }}
                    onPointerEnter={() => showPreview(historyId)}
                    onFocus={() => showPreview(historyId)}
                  >
                    <span className="conversation-trail-popover-mark" aria-hidden="true" />
                    <span className="conversation-trail-popover-summary">
                      {item.summary || t("chat.conversationTrailUntitled")}
                    </span>
                  </button>
                );
              })}
            </div>
          ) : null}
        </>
      )}
    </aside>
  );
}
