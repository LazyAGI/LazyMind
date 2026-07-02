import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
} from "react";
import type { ChatInputImperativeProps } from "../../ChatInput";

interface UseChatScrollOptions {
  chatInputRef: RefObject<ChatInputImperativeProps>;
  messageListLength: number;
  thinkingCollapseMap: Map<string, boolean>;
}

export function useChatScroll({
  chatInputRef,
  messageListLength,
  thinkingCollapseMap,
}: UseChatScrollOptions) {
  const chatContentRef = useRef<HTMLDivElement>(null);
  const isMouseScrollingRef = useRef(false);
  const [showScrollButton, setShowScrollButton] = useState(false);
  const [inputHeight, setInputHeight] = useState(120);

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

  const scrollToEndImmediately = useCallback(() => {
    isMouseScrollingRef.current = true;
    setShowScrollButton(false);
    const scroll = () => {
      const container = chatContentRef.current;
      if (container) {
        container.scrollTop = container.scrollHeight;
      }
    };
    requestAnimationFrame(() => {
      scroll();
      requestAnimationFrame(scroll);
      window.setTimeout(scroll, 80);
    });
  }, []);

  const scrollToEnd = useCallback(() => {
    if (!isMouseScrollingRef.current) {
      return;
    }
    scrollToEndImmediately();
  }, [scrollToEndImmediately]);

  const handleScroll = useCallback(() => {
    const metrics = getScrollMetrics();
    if (!metrics) {
      return;
    }

    setShowScrollButton(metrics.hasScrollbar && metrics.distance > 10);
    if (metrics.distance <= 10) {
      isMouseScrollingRef.current = true;
    } else {
      isMouseScrollingRef.current = false;
    }
  }, [getScrollMetrics]);

  const handleToBottom = useCallback(() => {
    const el = chatContentRef.current;
    if (!el) {
      return;
    }
    isMouseScrollingRef.current = true;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    setShowScrollButton(false);
  }, []);

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
    scrollToEnd,
    scrollToEndImmediately,
    handleScroll,
    handleToBottom,
    handleInputHeightChange,
  };
}
