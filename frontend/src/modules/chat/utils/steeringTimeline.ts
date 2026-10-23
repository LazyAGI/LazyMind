import { ChatConversationsResponseFinishReasonEnum as Finish } from "@/api/generated/chatbot-client";
import { RoleTypes } from "@/modules/chat/constants/common";

export interface SteeringBoundary {
  id: string;
  text: string;
  accepted_at?: number;
  assistant_segment?: string;
  reasoning_content?: string;
}

// Apply only server-ordered stream boundaries, never the HTTP acceptance time.
// Stable IDs allow both duplicate events and replay after reconnect.
export function applySteeringTimeline(
  source: any[], historyId: string, items: SteeringBoundary[], replay = false,
) {
  let list = [...source];
  if (replay) {
    const belongs = (m: any) => m.history_id === historyId || m.original_history_id === historyId;
    const first = list.findIndex((m) => belongs(m) && (m.role === RoleTypes.ASSISTANT || m.steering_message));
    const template = list.find((m) => belongs(m) && m.role === RoleTypes.ASSISTANT) || {};
    list = list.filter((m) => !belongs(m) || (m.role === RoleTypes.USER && !m.steering_message));
    list.splice(first < 0 ? list.length : first, 0, {
      ...template, id: undefined, history_id: historyId, original_history_id: undefined,
      steering_segment: undefined, steering_follow_up_id: undefined,
      role: RoleTypes.ASSISTANT, delta: "", raw_delta: "", reasoning_content: "",
    });
  }
  for (const item of items) {
    if (!item.id || !item.text || list.some((m) => m.steering_message && m.steering_follow_up_id === item.id)) continue;
    const index = list.findLastIndex((m) => m.role === RoleTypes.ASSISTANT && m.history_id === historyId);
    if (index < 0) continue;
    let previous = list[index];
    if (replay) previous = { ...previous, delta: item.assistant_segment || "", raw_delta: item.assistant_segment || "", reasoning_content: item.reasoning_content || "" };
    // A steering interruption can happen before the provider emits any
    // user-facing answer. In that case the optimistic assistant row may only
    // contain reasoning or a cancellation terminal. Do not freeze that row
    // between two user messages: the single continuation placeholder added
    // below is enough and matches the live task that is still running.
    //
    // Deliberately key this to steering boundaries only. Ordinary chat turns
    // keep their existing loading, reasoning and cancellation presentation.
    const visible = String(previous.display_delta || previous.delta || "").trim()
      || previous.ask_pending
      || previous.answers?.length || previous.images?.length || previous.files?.length;
    const additions: any[] = [];
    if (visible) additions.push({
      ...previous, id: `steering-segment:${item.id}`,
      history_id: `steering-segment:${historyId}:${item.id}`, original_history_id: historyId,
      steering_segment: true, steering_follow_up_id: item.id,
      run_status: undefined, run_terminal: undefined, model_retry: undefined,
      finish_reason: Finish.FinishReasonStop,
    });
    additions.push({
      id: `steering:${item.id}`, history_id: historyId, role: RoleTypes.USER,
      delta: item.text, display_delta: item.text, inputs: [{ input_type: "text", text: item.text }],
      finish_reason: Finish.FinishReasonStop, steering_message: true, is_resumed: true,
      steering_follow_up_id: item.id,
      create_time: item.accepted_at ? new Date(item.accepted_at * 1000).toISOString() : undefined,
    }, {
      history_id: historyId, role: RoleTypes.ASSISTANT, delta: "", raw_delta: "", reasoning_content: "",
      finish_reason: Finish.FinishReasonUnspecified, answers: [], sources: [],
      steering_continuation: true, steering_follow_up_id: item.id, model_route: previous.model_route,
    });
    list.splice(index, 1, ...additions);
  }
  return list;
}
