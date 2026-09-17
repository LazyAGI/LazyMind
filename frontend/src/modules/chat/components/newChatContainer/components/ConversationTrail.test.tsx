import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ConversationTrail from "./ConversationTrail";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      key === "chat.conversationTrailItem"
        ? `第 ${values?.index} 轮：${values?.summary}`
        : key,
  }),
}));

const items = Array.from({ length: 12 }, (_, index) => ({
  history_id: `history-${index + 1}`,
  seq: index + 1,
  summary: `问题摘要 ${index + 1}`,
  question: `完整用户问题 ${index + 1}\n补充说明`,
}));

function setup(count = 12) {
  const container = document.createElement("div");
  const targets = items.slice(0, count).map((item) => {
    const target = document.createElement("div");
    target.dataset.chatHistoryId = item.history_id;
    target.dataset.chatRole = "user";
    target.scrollIntoView = vi.fn();
    container.appendChild(target);
    return target;
  });
  return { container, targets, scrollContainerRef: { current: container } };
}

const node = (index: number) => screen.getByRole("button", {
  name: `第 ${index} 轮：问题摘要 ${index}`,
});

beforeEach(() => vi.useFakeTimers());
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

async function tick(ms: number) {
  await act(async () => { vi.advanceTimersByTime(ms); });
}

describe("ConversationTrail", () => {
  it("starts at three unique user turns and omits duplicate or missing anchors", () => {
    const props = { scrollContainerRef: { current: null }, messageListLength: 3 };
    const { rerender } = render(<ConversationTrail {...props} items={[items[0], items[1], items[0], { summary: "无锚点" }]} />);
    expect(screen.queryByLabelText("chat.conversationTrail")).not.toBeInTheDocument();
    rerender(<ConversationTrail {...props} items={[...items.slice(0, 3), items[0]]} />);
    expect(screen.getAllByRole("button", { name: /^第 \d+ 轮/ })).toHaveLength(3);
  });

  it("uses one ordered set of nodes and summaries, including turns beyond nine", () => {
    render(<ConversationTrail items={items} scrollContainerRef={{ current: null }} messageListLength={12} />);
    fireEvent.pointerEnter(node(2));
    expect(screen.getByLabelText("chat.conversationTrail")).toHaveClass("is-previewing");
    expect(screen.getAllByRole("button", { name: /^第 \d+ 轮/ })).toHaveLength(12);
    items.forEach((item, index) => expect(node(index + 1)).toHaveTextContent(item.summary));
    expect(screen.getAllByRole("button", { name: /^第 \d+ 轮/ }).map((button) => button.textContent)).toEqual(items.map((item) => item.summary));
    expect(node(12)).toHaveAttribute("aria-current", "true");
    expect(node(2)).not.toHaveAttribute("aria-current");
  });

  it("reveals the full question after dwelling, switches it with the summary and allows reading the card", async () => {
    render(<ConversationTrail items={items} scrollContainerRef={{ current: null }} messageListLength={12} />);
    fireEvent.pointerEnter(node(1));
    await tick(300);
    fireEvent.pointerEnter(node(2));
    await tick(300);
    expect(screen.queryByRole("region", { name: "chat.conversationTrailQuestion" })).not.toBeInTheDocument();
    await tick(200);
    let card = screen.getByRole("region", { name: "chat.conversationTrailQuestion" });
    expect(card).toHaveTextContent(items[1].question.replace("\n", " "));
    card.scrollTop = 100;
    fireEvent.pointerEnter(node(3));
    card = screen.getByRole("region", { name: "chat.conversationTrailQuestion" });
    expect(card).toHaveTextContent(items[2].question.replace("\n", " "));
    expect(card.scrollTop).toBe(0);
    fireEvent.pointerLeave(screen.getByLabelText("chat.conversationTrail"));
    fireEvent.pointerEnter(card);
    await tick(200);
    expect(card).toBeInTheDocument();
    fireEvent.keyDown(node(3), { key: "Escape" });
    expect(screen.queryByRole("region", { name: "chat.conversationTrailQuestion" })).not.toBeInTheDocument();
  });

  it("scrolls to the user turn and begins 1.5 second feedback only after scrolling settles", async () => {
    const { container, targets, scrollContainerRef } = setup();
    const onNavigate = vi.fn(() => expect(targets[0].scrollIntoView).not.toHaveBeenCalled());
    render(<ConversationTrail items={items} scrollContainerRef={scrollContainerRef} messageListLength={24} onNavigate={onNavigate} />);
    fireEvent.click(node(1));
    expect(onNavigate).toHaveBeenCalledOnce();
    expect(targets[0].scrollIntoView).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
    expect(targets[0]).not.toHaveClass("chat-item--trail-target");
    for (let index = 0; index < 5; index++) {
      fireEvent.scroll(container);
      await tick(80);
      expect(node(1)).toHaveAttribute("aria-current", "true");
      expect(targets[0]).not.toHaveClass("chat-item--trail-target");
    }
    fireEvent(container, new Event("scrollend"));
    expect(targets[0]).toHaveClass("chat-item--trail-target");
    await tick(1499);
    expect(targets[0]).toHaveClass("chat-item--trail-target");
    await tick(1);
    expect(targets[0]).not.toHaveClass("chat-item--trail-target");
  });

  it("uses a scroll-idle fallback and clears the old glow on rapid navigation and unmount", async () => {
    const { targets, scrollContainerRef } = setup();
    const { unmount } = render(<ConversationTrail items={items} scrollContainerRef={scrollContainerRef} messageListLength={24} />);
    fireEvent.click(node(1));
    await tick(200);
    expect(targets[0]).toHaveClass("chat-item--trail-target");
    fireEvent.click(node(2));
    expect(targets[0]).not.toHaveClass("chat-item--trail-target");
    await tick(200);
    expect(targets[1]).toHaveClass("chat-item--trail-target");
    unmount();
    expect(targets[1]).not.toHaveClass("chat-item--trail-target");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("respects reduced motion while retaining temporary location feedback", async () => {
    vi.spyOn(window, "matchMedia").mockReturnValue({ matches: true } as MediaQueryList);
    const { targets, scrollContainerRef } = setup(3);
    render(<ConversationTrail items={items.slice(0, 3)} scrollContainerRef={scrollContainerRef} messageListLength={6} />);
    fireEvent.click(node(2));
    expect(targets[1].scrollIntoView).toHaveBeenCalledWith({ behavior: "auto", block: "start" });
    await tick(200);
    expect(targets[1]).toHaveClass("chat-item--trail-target");
    await tick(1500);
    expect(targets[1]).not.toHaveClass("chat-item--trail-target");
  });

  it("tracks user messages rather than matching assistant messages when reading", async () => {
    const { container, targets, scrollContainerRef } = setup(3);
    Object.defineProperty(container, "clientHeight", { value: 500 });
    targets.forEach((target, index) => {
      vi.spyOn(target, "getBoundingClientRect").mockReturnValue({ top: index * 300 - 350 } as DOMRect);
      const assistant = document.createElement("div");
      assistant.dataset.chatHistoryId = target.dataset.chatHistoryId;
      assistant.dataset.chatRole = "assistant";
      vi.spyOn(assistant, "getBoundingClientRect").mockReturnValue({ top: -100 } as DOMRect);
      container.appendChild(assistant);
    });
    render(<ConversationTrail items={items.slice(0, 3)} scrollContainerRef={scrollContainerRef} messageListLength={6} />);
    fireEvent.scroll(container);
    await tick(20);
    expect(node(2)).toHaveAttribute("aria-current", "true");
    expect(node(3)).not.toHaveAttribute("aria-current");
  });

  it("loads an older turn and ignores a stale locate response after a newer click", async () => {
    const { container, targets, scrollContainerRef } = setup(3);
    targets[0].remove();
    let resolve!: (value: boolean) => void;
    const onLocate = vi.fn(() => new Promise<boolean>((done) => { resolve = done; }));
    render(<ConversationTrail items={items.slice(0, 3)} scrollContainerRef={scrollContainerRef} messageListLength={4} onLocate={onLocate} />);
    fireEvent.click(node(1));
    expect(node(1)).toHaveAttribute("aria-busy", "true");
    fireEvent.click(node(2));
    container.prepend(targets[0]);
    await act(async () => { resolve(true); });
    await tick(200);
    expect(targets[0].scrollIntoView).not.toHaveBeenCalled();
    expect(targets[1]).toHaveClass("chat-item--trail-target");
  });

  it("loads and locates an older user turn, allowing retry after a failed request", async () => {
    const { container, targets, scrollContainerRef } = setup(3);
    targets[0].remove();
    const onLocate = vi.fn().mockRejectedValueOnce(new Error("network unavailable")).mockImplementationOnce(async () => {
      container.prepend(targets[0]);
      return true;
    });
    render(<ConversationTrail items={items.slice(0, 3)} scrollContainerRef={scrollContainerRef} messageListLength={4} onLocate={onLocate} />);
    await act(async () => { fireEvent.click(node(1)); });
    expect(screen.getByRole("status")).toHaveTextContent("chat.fork.historyLoadFailed");
    await act(async () => { fireEvent.click(node(1)); });
    await tick(20);
    expect(targets[0].scrollIntoView).toHaveBeenCalled();
    await tick(200);
    expect(targets[0]).toHaveClass("chat-item--trail-target");
  });

  it("supports keyboard browsing and Escape while keeping the rail visible", async () => {
    render(<ConversationTrail items={items} scrollContainerRef={{ current: null }} messageListLength={12} />);
    act(() => node(1).focus());
    fireEvent.keyDown(node(1), { key: "ArrowDown" });
    expect(node(2)).toHaveFocus();
    fireEvent.keyDown(node(2), { key: "End" });
    expect(node(12)).toHaveFocus();
    fireEvent.keyDown(node(12), { key: "Home" });
    expect(node(1)).toHaveFocus();
    await tick(500);
    expect(screen.getByRole("region", { name: "chat.conversationTrailQuestion" })).toBeInTheDocument();
    fireEvent.keyDown(node(1), { key: "Escape" });
    expect(screen.queryByRole("region", { name: "chat.conversationTrailQuestion" })).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^第 \d+ 轮/ })).toHaveLength(12);
  });

  it("retains loaded nodes while refreshing and exposes the existing retry action", () => {
    const retry = vi.fn();
    render(<ConversationTrail items={items} scrollContainerRef={{ current: null }} messageListLength={12} loading error={new Error("refresh failed")} onRetry={retry} />);
    expect(screen.getAllByRole("button", { name: /^第 \d+ 轮/ })).toHaveLength(12);
    fireEvent.click(screen.getByRole("button", { name: "chat.conversationTrailRetry" }));
    expect(retry).toHaveBeenCalledOnce();
  });
});
