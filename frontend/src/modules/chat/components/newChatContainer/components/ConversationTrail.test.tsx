import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ConversationTrail from "./ConversationTrail";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      if (key === "chat.conversationTrailItem") {
        return `第 ${values?.index} 轮：${values?.summary}`;
      }
      return key;
    },
  }),
}));

describe("ConversationTrail", () => {
  it("locates a conversation turn and gives it temporary feedback", () => {
    const scrollContainer = document.createElement("div");
    const target = document.createElement("div");
    target.dataset.chatHistoryId = "history-1";
    target.scrollIntoView = vi.fn();
    scrollContainer.appendChild(target);
    const scrollContainerRef = { current: scrollContainer };

    render(
      <ConversationTrail
        items={[
          {
            history_id: "history-1",
            seq: 1,
            summary: "梳理项目现状",
            question: "梳理项目现状和后续计划",
            depth: 0,
          },
          {
            history_id: "history-2",
            seq: 2,
            summary: "补充接口设计",
            question: "补充接口设计",
            depth: 1,
          },
        ]}
        scrollContainerRef={scrollContainerRef}
        messageListLength={2}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "第 1 轮：梳理项目现状" }));

    expect(target.scrollIntoView).toHaveBeenCalledWith({
      behavior: "smooth",
      block: "start",
    });
    expect(target).toHaveClass("chat-item--trail-target");
  });

  it("supports keyboard-accessible collapse and expand controls", () => {
    render(
      <ConversationTrail
        items={[
          {
            history_id: "history-1",
            seq: 1,
            summary: "问题",
            question: "问题",
            depth: 0,
          },
        ]}
        scrollContainerRef={{ current: null }}
        messageListLength={1}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "chat.conversationTrailCollapse" }));
    expect(
      screen.getByRole("button", { name: "chat.conversationTrailExpand" }),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "chat.conversationTrailExpand" }),
    );
    expect(
      screen.getByRole("button", { name: "chat.conversationTrailCollapse" }),
    ).toBeInTheDocument();
  });

  it("keeps the compact rail visible while the summary preview is open", () => {
    render(
      <ConversationTrail
        items={[
          {
            history_id: "history-1",
            seq: 1,
            summary: "问题",
            question: "问题",
            depth: 0,
          },
        ]}
        scrollContainerRef={{ current: null }}
        messageListLength={1}
      />,
    );

    fireEvent.pointerEnter(
      screen.getByRole("button", { name: "第 1 轮：问题" }),
    );

    expect(document.querySelector(".conversation-trail")).toHaveClass(
      "is-previewing",
    );
    expect(
      document.querySelector(".conversation-trail-rail .conversation-trail-node span"),
    ).toBeInTheDocument();
    expect(screen.getByText("问题")).toBeInTheDocument();
  });

  it("uses a continuously decaying width profile around the current node", () => {
    render(
      <ConversationTrail
        items={[
          { history_id: "history-1", seq: 1, summary: "一", question: "一", depth: 0 },
          { history_id: "history-2", seq: 2, summary: "二", question: "二", depth: 0 },
          { history_id: "history-3", seq: 3, summary: "三", question: "三", depth: 0 },
        ]}
        scrollContainerRef={{ current: null }}
        messageListLength={3}
      />,
    );

    const nodes = document.querySelectorAll<HTMLElement>(
      ".conversation-trail-node",
    );
    expect(nodes[0]).toHaveAttribute("style", expect.stringContaining("--trail-width: 20px"));
    expect(nodes[1]).toHaveAttribute("style", expect.stringContaining("--trail-width: 24px"));
    expect(nodes[2]).toHaveAttribute("style", expect.stringContaining("--trail-width: 28px"));
  });

  it("moves the width peak to the hovered node", () => {
    render(
      <ConversationTrail
        items={[
          { history_id: "history-1", seq: 1, summary: "一", question: "一", depth: 0 },
          { history_id: "history-2", seq: 2, summary: "二", question: "二", depth: 0 },
          { history_id: "history-3", seq: 3, summary: "三", question: "三", depth: 0 },
        ]}
        scrollContainerRef={{ current: null }}
        messageListLength={3}
      />,
    );

    fireEvent.pointerEnter(
      screen.getByRole("button", { name: "第 2 轮：二" }),
    );

    const nodes = document.querySelectorAll<HTMLElement>(
      ".conversation-trail-node",
    );
    expect(nodes[0]).toHaveAttribute("style", expect.stringContaining("--trail-width: 24px"));
    expect(nodes[1]).toHaveAttribute("style", expect.stringContaining("--trail-width: 28px"));
    expect(nodes[2]).toHaveAttribute("style", expect.stringContaining("--trail-width: 24px"));
  });
});
