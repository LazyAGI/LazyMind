import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
} from "react";
import { RoleTypes } from "@/modules/chat/constants/common";
import type { ChatInputImperativeProps } from "../../ChatInput";

interface UseChatScrollOptions {
  chatInputRef: RefObject<ChatInputImperativeProps>;
  messageListLength: number;
  thinkingCollapseMap: Map<string, boolean>;
}

interface UnreadMessage {
  role?: string;
  history_id?: string;
  id?: string;
  delta?: string;
  reasoning_content?: string;
  answers?: { content?: string; reasoning_content?: string }[];
  ask_pending?: unknown;
  archived_failure?: boolean;
}

function contentSignature(item?: UnreadMessage) {
  if (!item) return "";
  const parts = [item.delta, item.reasoning_content,
    ...(item.answers || []).flatMap(answer => [answer.content, answer.reasoning_content]),
    item.ask_pending ? JSON.stringify(item.ask_pending) : "",
  ];
  return parts.some(Boolean) ? JSON.stringify(parts) : "";
}

export function useChatScroll({
  chatInputRef,
  messageListLength,
  thinkingCollapseMap,
}: UseChatScrollOptions) {
  const chatContentRef = useRef<HTMLDivElement>(null);
  const isMouseScrollingRef = useRef(true);
  const [showScrollButton, setShowScrollButton] = useState(false);
  const [inputHeight, setInputHeight] = useState(120);
  const [unreadCount, setUnreadCount] = useState(0);
  const unreadKeysRef = useRef(new Set<string>());
  const scrollFrameRef = useRef<number | null>(null);
  const lastScrollTopRef = useRef(0);
  const jumpingToBottomRef = useRef(false);

  const resetUnread = useCallback(() => {
    unreadKeysRef.current.clear();
    setUnreadCount(0);
  }, []);

  const trackNewContent = useCallback((previous: UnreadMessage[], next: UnreadMessage[]) => {
    if (isMouseScrollingRef.current) return;
    const assistants = previous.filter(item => item.role === RoleTypes.ASSISTANT && !item.archived_failure);
    const previousById = new Map(assistants.filter(item => item.history_id || item.id)
      .map(item => [item.history_id || item.id, item]));
    const lastKnownIndex = next.findLastIndex(item => item.role === RoleTypes.ASSISTANT && previousById.has(item.history_id || item.id));
    next.forEach((item, index) => {
      if (item.role !== RoleTypes.ASSISTANT || item.archived_failure) return;
      const key = item.history_id || item.id || "pending";
      const before = previousById.get(key) || (index === next.length - 1
        ? assistants.findLast(message => !message.history_id && !message.id) : undefined);
      if (before === item) return;
      // Loading an earlier page is not new generated content.
      if (!before && index < lastKnownIndex) return;
      if (before && !before.history_id && !before.id && key !== "pending" && unreadKeysRef.current.delete("pending")) {
        unreadKeysRef.current.add(key);
      }
      const signature = contentSignature(item);
      if (signature && signature !== contentSignature(before)) unreadKeysRef.current.add(key);
    });
    setUnreadCount(unreadKeysRef.current.size);
  }, []);

  const getScrollMetrics = useCallback(() => {
    const el = chatContentRef.current;
    if (!el) {
      return null;
    }

    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    return {
      distance,
      hasScrollbar: el.scrollHeight > el.clientHeight + 2,
    };
  }, []);

  const updateScrollButtonVisibility = useCallback(() => {
    const metrics = getScrollMetrics();
    if (!metrics) {
      return;
    }

    setShowScrollButton(metrics.hasScrollbar && metrics.distance > 10);
  }, [getScrollMetrics]);

  const pauseFollowing = useCallback(() => {
    isMouseScrollingRef.current = false;
    jumpingToBottomRef.current = false;
    if (scrollFrameRef.current !== null) cancelAnimationFrame(scrollFrameRef.current);
    scrollFrameRef.current = null;
  }, []);

  const scrollToEnd = useCallback(() => {
    if (!isMouseScrollingRef.current || jumpingToBottomRef.current || scrollFrameRef.current !== null) return;
    scrollFrameRef.current = requestAnimationFrame(() => {
      scrollFrameRef.current = null;
      const container = chatContentRef.current;
      if (!container || !isMouseScrollingRef.current) return;
      container.scrollTop = container.scrollHeight;
      lastScrollTopRef.current = container.scrollTop;
      updateScrollButtonVisibility();
      resetUnread();
    });
  }, [resetUnread, updateScrollButtonVisibility]);

  const scrollToEndImmediately = useCallback(() => {
    isMouseScrollingRef.current = true;
    jumpingToBottomRef.current = false;
    scrollToEnd();
  }, [scrollToEnd]);

  const handleScroll = useCallback(() => {
    const metrics = getScrollMetrics();
    const container = chatContentRef.current;
    if (!metrics || !container) return;
    setShowScrollButton(metrics.hasScrollbar && metrics.distance > 10);
    if (metrics.distance <= 10) {
      isMouseScrollingRef.current = true;
      jumpingToBottomRef.current = false;
      resetUnread();
    } else if (container.scrollTop < lastScrollTopRef.current - 1 ||
      (!jumpingToBottomRef.current && container.scrollTop !== lastScrollTopRef.current)) {
      pauseFollowing();
    }
    lastScrollTopRef.current = container.scrollTop;
  }, [getScrollMetrics, pauseFollowing, resetUnread]);

  const handleToBottom = useCallback(() => {
    const el = chatContentRef.current;
    if (!el) return;
    isMouseScrollingRef.current = true;
    jumpingToBottomRef.current = true;
    const reduceMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    el.scrollTo({ top: el.scrollHeight, behavior: reduceMotion ? "auto" : "smooth" });
  }, []);

  useEffect(() => {
    const container = chatContentRef.current;
    if (!container) return;
    lastScrollTopRef.current = container.scrollTop;
    const onWheel = (event: WheelEvent) => { if (event.deltaY < 0) pauseFollowing(); };
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.target as HTMLElement)?.closest("input, textarea, [contenteditable=true]")) return;
      if (["ArrowUp", "PageUp", "Home"].includes(event.key)) pauseFollowing();
    };
    const onTouchStart = () => pauseFollowing();
    const onScrollEnd = () => {
      if (!jumpingToBottomRef.current) return;
      jumpingToBottomRef.current = false;
      scrollToEnd();
    };
    const resize = new ResizeObserver(() => {
      updateScrollButtonVisibility();
      scrollToEnd();
    });
    const observeContent = () => {
      resize.disconnect();
      resize.observe(container);
      Array.from(container.children).forEach(child => resize.observe(child));
      updateScrollButtonVisibility();
      scrollToEnd();
    };
    const mutations = new MutationObserver(observeContent);
    mutations.observe(container, { childList: true, subtree: true, characterData: true });
    observeContent();
    container.addEventListener("wheel", onWheel, { passive: true });
    container.addEventListener("keydown", onKeyDown);
    container.addEventListener("scrollend", onScrollEnd);
    container.addEventListener("touchstart", onTouchStart, { passive: true });
    return () => {
      pauseFollowing();
      resize.disconnect();
      mutations.disconnect();
      container.removeEventListener("wheel", onWheel);
      container.removeEventListener("keydown", onKeyDown);
      container.removeEventListener("scrollend", onScrollEnd);
      container.removeEventListener("touchstart", onTouchStart);
    };
  }, [pauseFollowing, scrollToEnd, updateScrollButtonVisibility]);

  const syncInputHeight = useCallback(() => {
    const inputElement = chatInputRef.current?.element;
    if (inputElement) {
      const height = inputElement.offsetHeight;
      setInputHeight(height + 20);
      document.documentElement.style.setProperty(
        "--chat-input-height",
        `${height + 20}px`,
      );
    }
  }, [chatInputRef]);

  const handleInputHeightChange = useCallback(() => {
    syncInputHeight();
  }, [syncInputHeight]);

  useEffect(() => {
    const rafId = requestAnimationFrame(() => {
      updateScrollButtonVisibility();
    });

    return () => cancelAnimationFrame(rafId);
  }, [
    messageListLength,
    thinkingCollapseMap,
    inputHeight,
    updateScrollButtonVisibility,
  ]);

  useEffect(() => {
    syncInputHeight();
    window.addEventListener("resize", syncInputHeight);

    const observer = new MutationObserver(() => {
      syncInputHeight();
    });

    if (chatInputRef.current?.element) {
      observer.observe(chatInputRef.current.element, {
        attributes: true,
        childList: true,
        subtree: true,
        attributeFilter: ["style", "class"],
      });
    }

    return () => {
      window.removeEventListener("resize", syncInputHeight);
      observer.disconnect();
    };
  }, [chatInputRef, syncInputHeight]);

  return {
    chatContentRef,
    isMouseScrollingRef,
    showScrollButton,
    inputHeight,
    unreadCount,
    resetUnread,
    trackNewContent,
    pauseFollowing,
    scrollToEnd,
    scrollToEndImmediately,
    handleScroll,
    handleToBottom,
    handleInputHeightChange,
  };
}
