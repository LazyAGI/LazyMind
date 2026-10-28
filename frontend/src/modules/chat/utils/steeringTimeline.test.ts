import { describe, it, expect } from "vitest";
import { RoleTypes as Role } from "@/modules/chat/constants/common";
import { applySteeringTimeline } from "./steeringTimeline";
import { buildChatMessageListFromHistory } from "./message";

describe("ordered steering transcript", () => {
  const initial = [
    { role: Role.USER, history_id: "h", delta: "周末从北京出发" },
    { role: Role.ASSISTANT, history_id: "h", delta: "原始部分回复" },
  ];
  const first = { id: "a", text: "只比较天津和济南", accepted_at: 1 };
  const second = { id: "b", text: "预算2000元", accepted_at: 2 };

  it("keeps partial replies, avoids duplicates and restores identical IDs after reload", () => {
    let live = applySteeringTimeline(initial, "h", [first]);
    live[live.length - 1] = { ...live[live.length - 1], delta: "第一条引导的部分回复" };
    live = applySteeringTimeline(live, "h", [second]);
    expect(applySteeringTimeline(live, "h", [second])).toEqual(live);
    const history = buildChatMessageListFromHistory([{
      id: "h", query: "周末从北京出发", result: "原始部分回复第一条引导的部分回复最终回复",
      assistant_remainder: "最终回复", steering_messages: [
        { ...first, accepted_at: undefined, assistant_segment: "原始部分回复" },
        { ...second, accepted_at: undefined, assistant_segment: "第一条引导的部分回复" },
      ],
    }]);
    expect(history.map(m => m.delta)).toEqual(["周末从北京出发", "原始部分回复", first.text, "第一条引导的部分回复", second.text, "最终回复"]);
    expect(history.slice(1, -1).map(m => m.id)).toEqual(live.slice(1, -1).map(m => m.id));
  });

  it("replays onto loaded history without duplicating segments or other conversations", () => {
    const unrelated = { role: Role.ASSISTANT, history_id: "other", delta: "other answer" };
    const replay = [{ ...first, assistant_segment: "原始部分回复" }, { ...second, assistant_segment: "新片段" }];
    let list = applySteeringTimeline([...initial, unrelated], "h", replay, true);
    list = applySteeringTimeline(list, "h", replay, true);
    expect(list.map(m => m.delta)).toEqual(["周末从北京出发", "原始部分回复", first.text, "新片段", second.text, "", "other answer"]);
  });

  it("moves the only loading placeholder after a burst before first output", () => {
    const list = applySteeringTimeline([initial[0], { ...initial[1], delta: "" }], "h", [first, second]);
    expect(list.map(m => m.role)).toEqual([Role.USER, Role.USER, Role.USER, Role.ASSISTANT]);
  });

  it("does not preserve reasoning or cancellation placeholders before first visible output", () => {
    const placeholder = {
      ...initial[1],
      delta: "",
      raw_delta: "<think>planning</think>",
      reasoning_content: "planning",
      run_status: "cancelled",
      run_terminal: { status: "cancelled", reason: "user_cancelled" },
    };

    const list = applySteeringTimeline([initial[0], placeholder], "h", [first, second]);

    expect(list.map(m => [m.role, m.delta])).toEqual([
      [Role.USER, "周末从北京出发"],
      [Role.USER, first.text],
      [Role.USER, second.text],
      [Role.ASSISTANT, ""],
    ]);
    expect(list.some(m => m.reasoning_content === "planning")).toBe(false);
    expect(list.some(m => m.run_status === "cancelled")).toBe(false);
  });
});
