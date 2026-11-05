// TEMPORARY local presentation-only replay. Never use source history IDs for actions.
import { useEffect, useMemo, useState } from "react";
import { ChatServiceApi } from "./request";
import { buildChatMessageListFromHistory } from "./message";
import { useWorkflowStore } from "../store/workflowPanel";
import { useTaskCenterStore } from "../store/taskCenter";

export function useLocalPptDemoTranscript(conversationId: string, actual: any[]) {
  const session = useWorkflowStore(s => s.sessionByConversation[conversationId]);
  const tasks = useTaskCenterStore(s => s.tasksByConversation[conversationId]);
  const enabled = ["localhost", "127.0.0.1"].includes(window.location.hostname)
    && Boolean(session?.session_id.startsWith("pptdemo-"));
  const [records, setRecords] = useState<any[] | null>(null);
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    // Use the normal authenticated API: source access remains owner-checked.
    void ChatServiceApi().conversationServiceGetConversationHistory({
      name: "0ad45c90-9043-4b23-a144-f77650479272",
    }).then(response => {
      if (!cancelled) setRecords([...(response.data.history ?? [])].sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0)));
    }).catch(() => { if (!cancelled) setRecords(null); });
    return () => { cancelled = true; };
  }, [enabled]);
  return useMemo(() => {
    if (!enabled || !records) return actual;
    const done = new Set(session?.steps.filter(s => s.status === "succeeded").map(s => s.step_id));
    const edited = tasks?.some(t => t.agent_type === "pptx_edit");
    const count = edited ? 5 : done.has("generate_ppt") ? 4
      : done.has("build_outline") ? 3 : done.has("collect_materials") ? 2 : 1;
    const history = records.slice(0, count).map(record => ({
      ...record,
      // Explicitly inert identity; never edit/retry the source conversation.
      id: `pptdemo-history-${record.id}`,
      run_id: undefined,
      failed_attempts: [],
      fork_read_only: true,
      ask_answered: true,
      model_route: { ...record.model_route, provider_name: "", model_name: "Deepseek-v4-pro" },
    }));
    return buildChatMessageListFromHistory(history, { reverseHistory: false }).map(item => ({
      ...item, local_ppt_demo: true, fork_read_only: true,
    }));
  }, [enabled, records, actual, session, tasks]);
}
