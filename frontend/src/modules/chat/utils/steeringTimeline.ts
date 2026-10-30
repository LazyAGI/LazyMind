import { ChatConversationsResponseFinishReasonEnum as Finish } from "@/api/generated/chatbot-client";
import { RoleTypes } from "@/modules/chat/constants/common";
import { splitModelOutputContent } from "./thinking";

export interface SteeringIntentUpdate {
  scope: string;
  intent_context?: Record<string, unknown>;
  steering_segment_id?: string;
}

// Ownership follows applied user boundaries, not the last rendered assistant.
// Missing attribution belongs to legacy records and keeps their old behavior.
export function assignSteeringIntent(source: any[], historyId: string, intent?: SteeringIntentUpdate) {
  if (typeof intent?.steering_segment_id !== "string") return source;
  let segment = "";
  return source.map((message) => {
    if (message.history_id !== historyId && !(message.steering_segment && message.original_history_id === historyId)) return message;
    if (message.steering_message && !message.steering_pending) segment = message.steering_follow_up_id;
    if (message.role !== RoleTypes.ASSISTANT) return message;
    let update = message.intent_updated;
    if (segment === intent.steering_segment_id) update = intent;
    else if (update?.steering_segment_id === intent.steering_segment_id) update = undefined;
    return { ...message, intent_updated: update };
  });
}

export interface SteeringBoundary {
  id: string;
  text: string;
  accepted_at?: number;
  assistant_segment?: string;
  reasoning_content?: string;
  intent_updated?: SteeringIntentUpdate;
}

export function appendAcceptedSteering(source: any[], historyId: string, item: SteeringBoundary) {
  if (!historyId || source.some((m) => m.steering_message && m.steering_follow_up_id === item.id)) return source;
  const index = source.findLastIndex((m) => m.role === RoleTypes.ASSISTANT && m.history_id === historyId);
  if (index < 0) return source;
  // Acceptance is visible immediately, but only the joined stream boundary
  // may freeze the old assistant segment. Never guess a boundary from HTTP.
  const list = [...source];
  let position = index + 1;
  while (list[position]?.steering_pending && list[position]?.history_id === historyId) position += 1;
  list.splice(position, 0, steeringUserMessage(historyId, item, true));
  return list;
}

function steeringUserMessage(historyId: string, item: SteeringBoundary, pending = false) {
  return {
    id: `steering:${item.id}`, history_id: historyId, role: RoleTypes.USER,
    delta: item.text, display_delta: item.text, inputs: [{ input_type: "text", text: item.text }],
    finish_reason: Finish.FinishReasonStop, steering_message: true, is_resumed: true,
    steering_follow_up_id: item.id, steering_pending: pending,
    create_time: item.accepted_at ? new Date(item.accepted_at * 1000).toISOString() : undefined,
  };
}

// Apply only server-ordered stream boundaries, never the HTTP acceptance time.
// Stable IDs allow both duplicate events and replay after reconnect.
export function applySteeringTimeline(
  source: any[], historyId: string, items: SteeringBoundary[], replay = false, modelOutput?: unknown,
) {
  let list = [...source];
  const intent = replay ? source.findLast((m) => (m.history_id === historyId || m.original_history_id === historyId) && m.intent_updated)?.intent_updated : undefined;
  if (replay) {
    const belongs = (m: any) => m.history_id === historyId || m.original_history_id === historyId;
    const first = list.findIndex((m) => belongs(m) && (m.role === RoleTypes.ASSISTANT || m.steering_message));
    const template = list.find((m) => belongs(m) && m.role === RoleTypes.ASSISTANT) || {};
    list = list.filter((m) => !belongs(m) || (m.role === RoleTypes.USER && !m.steering_message));
    list.splice(first < 0 ? list.length : first, 0, {
      ...template, id: undefined, history_id: historyId, original_history_id: undefined,
      model_output: modelOutput || template.model_output,
      steering_segment: undefined, steering_follow_up_id: undefined,
      role: RoleTypes.ASSISTANT, delta: "", raw_delta: "", reasoning_content: "",
    });
  }
  for (const item of items) {
    if (!item.id || !item.text || list.some((m) => m.steering_message && !m.steering_pending && m.steering_follow_up_id === item.id)) continue;
    list = list.filter((m) => !(m.steering_pending && m.steering_follow_up_id === item.id));
    const index = list.findLastIndex((m) => m.role === RoleTypes.ASSISTANT && m.history_id === historyId);
    if (index < 0) continue;
    let previous = list[index];
    if (replay) {
      const split = previous.model_output
        ? splitModelOutputContent(item.assistant_segment || "", item.reasoning_content)
        : { content: item.assistant_segment || "", reasoning_content: item.reasoning_content || "" };
      previous = { ...previous, delta: split.content, raw_delta: item.assistant_segment || "", reasoning_content: split.reasoning_content, intent_updated: item.intent_updated };
    }
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
    additions.push(steeringUserMessage(historyId, item), {
      history_id: historyId, role: RoleTypes.ASSISTANT, delta: "", raw_delta: "", reasoning_content: "",
      finish_reason: Finish.FinishReasonUnspecified, answers: [], sources: [],
      steering_continuation: true, steering_follow_up_id: item.id, model_route: previous.model_route,
      model_output: previous.model_output,
    });
    list.splice(index, 1, ...additions);
  }
  return assignSteeringIntent(list, historyId, intent);
}
