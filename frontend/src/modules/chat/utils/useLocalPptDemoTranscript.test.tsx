import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
const state = vi.hoisted(() => ({
  workflow: { sessionByConversation: {} as Record<string, any> },
  tasks: { tasksByConversation: {} as Record<string, any[]> },
  fetch: vi.fn(),
}));
vi.mock("./request", () => ({ ChatServiceApi: () => ({ conversationServiceGetConversationHistory: state.fetch }) }));
vi.mock("../store/workflowPanel", () => ({ useWorkflowStore: (select: any) => select(state.workflow) }));
vi.mock("../store/taskCenter", () => ({ useTaskCenterStore: (select: any) => select(state.tasks) }));
import { useLocalPptDemoTranscript } from "./useLocalPptDemoTranscript";

beforeEach(() => {
  state.workflow.sessionByConversation = {};
  state.tasks.tasksByConversation = {};
  state.fetch.mockReset();
  state.fetch.mockResolvedValue({ data: { history: [1, 2, 3, 4, 5].map(seq => ({
    id: `source-${seq}`, seq, query: `原问题${seq}`, result: `<tp>原思考${seq}</tp>原回复${seq}`,
    thinking_time_s: seq * 59, create_time: "2026-10-19T08:11:04Z", run_status: "completed",
  })) } });
});

it("ordinary chats are untouched and do not fetch source history", () => {
  const actual = [{ delta: "normal" }];
  const { result } = renderHook(() => useLocalPptDemoTranscript("ordinary", actual));
  expect(result.current).toBe(actual);
  expect(state.fetch).not.toHaveBeenCalled();
});

it("reveals original turns with recorded thinking/time and inert IDs, then final edit", async () => {
  const session = { session_id: "pptdemo-test", steps: [] as any[] };
  state.workflow.sessionByConversation.demo = session;
  const { result, rerender } = renderHook(() => useLocalPptDemoTranscript("demo", []));
  await waitFor(() => expect(result.current).toHaveLength(2));
  expect(result.current[0]).toMatchObject({ delta: "原问题1", create_time: "2026-10-19T08:11:04Z" });
  expect(result.current[1]).toMatchObject({ delta: "原回复1", reasoning_content: "<tp>原思考1</tp>", thinking_time_s: 59 });
  expect(result.current[1].history_id).toBe("pptdemo-history-source-1");
  state.workflow.sessionByConversation.demo = { ...session, steps: [{step_id: "generate_ppt", status: "succeeded"}] };
  rerender();
  expect(result.current).toHaveLength(8);
  state.tasks.tasksByConversation.demo = [{agent_type: "pptx_edit"}];
  rerender();
  expect(result.current).toHaveLength(10);
  expect(result.current[9].thinking_time_s).toBe(295);
});
