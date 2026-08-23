import { useCallback, useEffect, useState } from "react";
import type { ChatMention } from "@/modules/chat/components/ChatInput/MentionEditor";
import { enableBuiltinSkill } from "@/modules/memory/skillApi";

type FeaturedSkillStatus = "idle" | "preparing" | "ready" | "failed";

export function useFeaturedSkillBinding(builtinSkillUID?: string) {
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<FeaturedSkillStatus>("idle");
  const [mentions, setMentions] = useState<ChatMention[]>([]);

  useEffect(() => {
    let active = true;
    setMentions([]);
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
  }, [attempt, builtinSkillUID]);

  const retry = useCallback(() => setAttempt((current) => current + 1), []);
  return { mentions, retry, status };
}
