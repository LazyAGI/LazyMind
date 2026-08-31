import { useCallback, useEffect, useState } from "react";
import type { ChatMention } from "@/modules/chat/components/ChatInput/MentionEditor";
import { enableBuiltinSkill } from "@/modules/memory/skillApi";
import type { ShowcaseCase } from "./api";

type FeaturedCapabilityBinding = Pick<
  ShowcaseCase,
  "type" | "title" | "builtin_skill_uid" | "workflow_ref"
>;
type FeaturedCapabilityStatus = "idle" | "preparing" | "ready" | "failed";

export function useFeaturedCapabilityBinding(
  capability?: FeaturedCapabilityBinding | null,
) {
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<FeaturedCapabilityStatus>("idle");
  const [mentions, setMentions] = useState<ChatMention[]>([]);
  const capabilityType = capability?.type;
  const capabilityTitle = capability?.title;
  const builtinSkillUID = capability?.builtin_skill_uid;
  const workflowRef = capability?.workflow_ref;

  useEffect(() => {
    let active = true;
    setMentions([]);

    if (!capabilityType) {
      setStatus("idle");
      return () => { active = false; };
    }

    if (capabilityType === "workflow") {
      if (!workflowRef) {
        console.error("Prepare featured Workflow failed: missing workflow_ref");
        setStatus("failed");
      } else {
        setMentions([{
          mention_id: `featured-workflow:${workflowRef}`,
          type: "workflow",
          resource_id: workflowRef,
          display_name: capabilityTitle || workflowRef,
        }]);
        setStatus("ready");
      }
      return () => { active = false; };
    }

    if (!builtinSkillUID) {
      setStatus("idle");
      return () => { active = false; };
    }

    setStatus("preparing");
    void enableBuiltinSkill(builtinSkillUID)
      .then((installed) => {
        if (!active) return;
        if (!installed?.skillId) {
          throw new Error("builtin Skill install returned no skill id");
        }
        setMentions([{
          mention_id: `featured:${builtinSkillUID}:${installed.skillId}`,
          type: "skill",
          resource_id: installed.skillId,
          display_name: installed.name,
        }]);
        setStatus("ready");
      })
      .catch((error) => {
        if (!active) return;
        console.error("Prepare featured Skill failed:", error);
        setStatus("failed");
      });

    return () => { active = false; };
  }, [attempt, builtinSkillUID, capabilityTitle, capabilityType, workflowRef]);

  const retry = useCallback(() => setAttempt((current) => current + 1), []);
  return { mentions, retry, status };
}
