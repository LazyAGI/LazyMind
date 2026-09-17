import { act, fireEvent, renderHook } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useChatScroll } from "./useChatScroll";
import type { ChatInputImperativeProps } from "../../ChatInput";
import { RoleTypes } from "@/modules/chat/constants/common";

const answer = (history_id: string, delta: string) => ({ history_id, delta, role: RoleTypes.ASSISTANT });

function setup() {
  const element = document.createElement("div");
  document.body.appendChild(element);
  let height = 1000;
  Object.defineProperties(element, {
    scrollHeight: { get: () => height },
    clientHeight: { value: 400 },
    scrollTop: { value: 600, writable: true },
  });
  element.scrollTo = vi.fn(({ top }: ScrollToOptions) => { element.scrollTop = Math.min(top || 0, height - 400); });
  const chatInputRef = createRef<ChatInputImperativeProps>();
  const { result, rerender, unmount } = renderHook(() => {
    const scroll = useChatScroll({ chatInputRef, messageListLength: 2, thinkingCollapseMap: new Map() });
    Object.assign(scroll.chatContentRef, { current: element });
    return scroll;
  });
  act(() => result.current.handleScroll());
  return { result, rerender, unmount, element, grow: () => { height += 200; } };
}

describe("useChatScroll reading position", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => setTimeout(() => callback(0), 16));
    vi.stubGlobal("cancelAnimationFrame", clearTimeout);
  });
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("cancels queued following as soon as the user scrolls upward", () => {
    const { result, element, grow } = setup();
    act(() => { grow(); result.current.scrollToEnd(); });
    element.scrollTop = 200;
    act(() => { fireEvent.wheel(element, { deltaY: -120 }); result.current.handleScroll(); vi.runAllTimers(); });
    expect(element.scrollTop).toBe(200);
    expect(result.current.showScrollButton).toBe(true);
  });

  it("follows content only while at the latest position", () => {
    const { result, element, grow } = setup();
    act(() => { grow(); result.current.scrollToEnd(); vi.runAllTimers(); });
    expect(element.scrollTop).toBeGreaterThanOrEqual(800);
    element.scrollTop = 150;
    act(() => result.current.handleScroll());
    act(() => { grow(); result.current.scrollToEnd(); vi.runAllTimers(); });
    expect(element.scrollTop).toBe(150);
  });

  it("counts each updated answer once, ignores metadata and supports multiple answers", () => {
    const { result, element } = setup();
    element.scrollTop = 100;
    act(() => result.current.handleScroll());
    const first = answer("h1", "first");
    const longer = answer("h1", "first and more");
    const second = answer("h2", "second");
    const completed = { ...longer, finish_reason: "stop" };
    act(() => {
      result.current.trackNewContent([answer("h1", "")], [first]);
      result.current.trackNewContent([first], [longer]);
      result.current.trackNewContent([longer], [completed]);
      result.current.trackNewContent([longer], [longer, second]);
    });
    expect(result.current.unreadCount).toBe(2);
    expect(element.scrollTop).toBe(100);
    act(() => result.current.trackNewContent([longer, second], [longer, second]));
    expect(result.current.unreadCount).toBe(2);
  });

  it("clears unread only after reaching the bottom and resumes following", () => {
    const { result, element } = setup();
    element.scrollTop = 100;
    act(() => { result.current.handleScroll(); result.current.trackNewContent([], [answer("h1", "new")]); });
    expect(result.current.unreadCount).toBe(1);
    act(() => { result.current.handleToBottom(); result.current.handleScroll(); });
    expect(element.scrollTo).toHaveBeenCalled();
    expect(result.current.unreadCount).toBe(0);
    expect(result.current.showScrollButton).toBe(false);
    act(() => result.current.trackNewContent([], [answer("h2", "visible")]));
    expect(result.current.unreadCount).toBe(0);
  });

  it("counts continued content again after the earlier part was read", () => {
    const { result, element } = setup();
    const before = answer("h1", "read");
    element.scrollTop = 100;
    act(() => { result.current.handleScroll(); result.current.trackNewContent([before], [answer("h1", "new")]); });
    act(() => { element.scrollTop = 600; result.current.handleScroll(); });
    expect(result.current.unreadCount).toBe(0);
    act(() => { element.scrollTop = 100; result.current.handleScroll(); result.current.trackNewContent([answer("h1", "new")], [answer("h1", "newer")]); });
    expect(result.current.unreadCount).toBe(1);
  });

  it("does not count old history or a placeholder receiving its persisted id", () => {
    const { result, element } = setup();
    element.scrollTop = 100;
    act(() => result.current.handleScroll());
    const placeholder = { role: RoleTypes.ASSISTANT, delta: "draft" };
    const persisted = answer("h2", "draft");
    act(() => result.current.trackNewContent([placeholder], [persisted]));
    expect(result.current.unreadCount).toBe(0);
    act(() => result.current.trackNewContent([persisted], [answer("h1", "old"), persisted]));
    expect(result.current.unreadCount).toBe(0);
    act(() => result.current.trackNewContent([persisted], [answer("h2", "draft grows")]));
    expect(result.current.unreadCount).toBe(1);
    act(() => result.current.resetUnread());
    expect(result.current.unreadCount).toBe(0);
  });

  it("does not run queued scroll work after unmount", () => {
    const { result, element, unmount } = setup();
    act(() => result.current.scrollToEndImmediately());
    unmount();
    element.scrollTop = 100;
    act(() => vi.runAllTimers());
    expect(element.scrollTop).toBe(100);
  });
});
