import { act, renderHook } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatInputImperativeProps } from "../../ChatInput";
import { useChatScroll } from "./useChatScroll";

function makeFakeContentEl(overrides: Partial<HTMLDivElement> = {}) {
  return {
    scrollHeight: 1000,
    scrollTop: 0,
    clientHeight: 500,
    scrollTo: vi.fn(),
    ...overrides,
  } as unknown as HTMLDivElement;
}

function setup() {
  const chatInputRef = createRef<ChatInputImperativeProps>();
  const { result, rerender } = renderHook(
    ({ messageListLength, thinkingCollapseMap }) =>
      useChatScroll({ chatInputRef, messageListLength, thinkingCollapseMap }),
    {
      initialProps: {
        messageListLength: 0,
        thinkingCollapseMap: new Map<string, boolean>(),
      },
    },
  );
  return { result, rerender, chatInputRef };
}

describe("useChatScroll", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "requestAnimationFrame"] });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns a false showScrollButton and default inputHeight initially", () => {
    const { result } = setup();
    expect(result.current.showScrollButton).toBe(false);
    expect(result.current.inputHeight).toBe(120);
  });

  it("handleScroll shows the scroll button when far from the bottom with a scrollbar", () => {
    const { result } = setup();
    act(() => {
      result.current.chatContentRef.current = makeFakeContentEl({
        scrollHeight: 1000,
        scrollTop: 0,
        clientHeight: 500,
      });
    });

    act(() => {
      result.current.handleScroll();
    });

    expect(result.current.showScrollButton).toBe(true);
    expect(result.current.isMouseScrollingRef.current).toBe(false);
  });

  it("handleScroll hides the button and marks mouse scrolling when near the bottom", () => {
    const { result } = setup();
    act(() => {
      result.current.chatContentRef.current = makeFakeContentEl({
        scrollHeight: 1000,
        scrollTop: 995,
        clientHeight: 500,
      });
    });

    act(() => {
      result.current.handleScroll();
    });

    expect(result.current.showScrollButton).toBe(false);
    expect(result.current.isMouseScrollingRef.current).toBe(true);
  });

  it("handleScroll is a no-op when there is no content element yet", () => {
    const { result } = setup();
    act(() => {
      result.current.handleScroll();
    });
    expect(result.current.showScrollButton).toBe(false);
  });

  it("scrollToEnd does nothing unless isMouseScrollingRef is true", () => {
    const { result } = setup();
    const el = makeFakeContentEl({ scrollHeight: 800, scrollTop: 0 });
    act(() => {
      result.current.chatContentRef.current = el;
    });

    act(() => {
      result.current.scrollToEnd();
      vi.runAllTimers();
    });

    expect(el.scrollTop).toBe(0);
  });

  it("scrollToEndImmediately hides the button and forces scrollTop to scrollHeight", () => {
    const { result } = setup();
    // no scrollbar, so the mount effect's own visibility check stays false
    const el = makeFakeContentEl({ scrollHeight: 500, scrollTop: 0, clientHeight: 500 });
    act(() => {
      result.current.chatContentRef.current = el;
      vi.runAllTimers();
    });

    act(() => {
      result.current.scrollToEndImmediately();
      vi.runAllTimers();
    });

    expect(result.current.showScrollButton).toBe(false);
    expect(result.current.isMouseScrollingRef.current).toBe(true);
    expect(el.scrollTop).toBe(500);
  });

  it("handleToBottom calls scrollTo smoothly and hides the scroll button", () => {
    const { result } = setup();
    const el = makeFakeContentEl({ scrollHeight: 900 });
    act(() => {
      result.current.chatContentRef.current = el;
    });

    act(() => {
      result.current.handleToBottom();
    });

    expect(el.scrollTo).toHaveBeenCalledWith({ top: 900, behavior: "smooth" });
    expect(result.current.showScrollButton).toBe(false);
    expect(result.current.isMouseScrollingRef.current).toBe(true);
  });

  it("handleToBottom is a no-op when there is no content element", () => {
    const { result } = setup();
    expect(() => {
      act(() => {
        result.current.handleToBottom();
      });
    }).not.toThrow();
  });

  it("handleInputHeightChange updates inputHeight based on the chat input element height", () => {
    const chatInputRef = createRef<ChatInputImperativeProps>();
    const element = document.createElement("div");
    Object.defineProperty(element, "offsetHeight", {
      value: 60,
      configurable: true,
    });
    document.body.appendChild(element);
    chatInputRef.current = { element } as unknown as ChatInputImperativeProps;

    const { result } = renderHook(() =>
      useChatScroll({
        chatInputRef,
        messageListLength: 0,
        thinkingCollapseMap: new Map(),
      }),
    );

    act(() => {
      Object.defineProperty(element, "offsetHeight", {
        value: 100,
        configurable: true,
      });
      result.current.handleInputHeightChange();
    });

    expect(result.current.inputHeight).toBe(120);
    expect(
      document.documentElement.style.getPropertyValue("--chat-input-height"),
    ).toBe("120px");

    document.body.removeChild(element);
  });
});
