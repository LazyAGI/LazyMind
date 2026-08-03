import { act, renderHook } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ChatInputImperativeProps } from "../../ChatInput";
import { MAX_CITE_MESSAGE_COUNT } from "../utils/citeMessage";
import { useCiteMessagesInput } from "./useCiteMessagesInput";

const { warningMock } = vi.hoisted(() => ({
  warningMock: vi.fn(),
}));

vi.mock("antd", () => ({
  message: { warning: warningMock },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      values ? `${key}:${JSON.stringify(values)}` : key,
  }),
}));

function setup() {
  const chatInputRef = createRef<ChatInputImperativeProps>();
  chatInputRef.current = { focus: vi.fn() } as unknown as ChatInputImperativeProps;
  const { result } = renderHook(() => useCiteMessagesInput(chatInputRef));
  return { result, chatInputRef };
}

describe("useCiteMessagesInput", () => {
  it("starts with empty cite messages and history ids", () => {
    const { result } = setup();
    expect(result.current.citeMessages).toEqual([]);
    expect(result.current.citeHistoryIds).toEqual([]);
  });

  it("adds a trimmed cite message with its history id", () => {
    const { result } = setup();

    act(() => {
      result.current.handleAddCiteMessage("  hello world  ", "history-1");
    });

    expect(result.current.citeMessages).toEqual(["hello world"]);
    expect(result.current.citeHistoryIds).toEqual(["history-1"]);
  });

  it("ignores blank text and does not add anything", () => {
    const { result } = setup();

    act(() => {
      result.current.handleAddCiteMessage("   ");
    });

    expect(result.current.citeMessages).toEqual([]);
    expect(result.current.citeHistoryIds).toEqual([]);
  });

  it("stores undefined history id when historyId is blank", () => {
    const { result } = setup();

    act(() => {
      result.current.handleAddCiteMessage("hello", "   ");
    });

    expect(result.current.citeHistoryIds).toEqual([undefined]);
  });

  it("warns and stops adding once MAX_CITE_MESSAGE_COUNT is reached", () => {
    const { result } = setup();

    act(() => {
      for (let i = 0; i < MAX_CITE_MESSAGE_COUNT; i += 1) {
        result.current.handleAddCiteMessage(`message-${i}`);
      }
    });
    expect(result.current.citeMessages).toHaveLength(MAX_CITE_MESSAGE_COUNT);

    act(() => {
      result.current.handleAddCiteMessage("overflow");
    });

    expect(result.current.citeMessages).toHaveLength(MAX_CITE_MESSAGE_COUNT);
    expect(warningMock).toHaveBeenCalledTimes(1);
  });

  it("removes a cite message and its history id at the given index", () => {
    const { result } = setup();

    act(() => {
      result.current.handleAddCiteMessage("first", "h1");
      result.current.handleAddCiteMessage("second", "h2");
    });

    act(() => {
      result.current.handleRemoveCiteMessage(0);
    });

    expect(result.current.citeMessages).toEqual(["second"]);
    expect(result.current.citeHistoryIds).toEqual(["h2"]);
  });

  it("clears all cite messages and history ids", () => {
    const { result } = setup();

    act(() => {
      result.current.handleAddCiteMessage("first", "h1");
      result.current.handleAddCiteMessage("second", "h2");
    });

    act(() => {
      result.current.clearCiteMessages();
    });

    expect(result.current.citeMessages).toEqual([]);
    expect(result.current.citeHistoryIds).toEqual([]);
  });
});
