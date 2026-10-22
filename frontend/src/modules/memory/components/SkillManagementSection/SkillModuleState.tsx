import { Button } from "antd";
import type { ReactNode } from "react";

interface SkillModuleStateProps {
  tone: "loading" | "error" | "success" | "warning";
  children: ReactNode;
  retryLabel?: string;
  onRetry?: () => void;
}

export default function SkillModuleState({
  tone,
  children,
  retryLabel,
  onRetry,
}: SkillModuleStateProps) {
  return (
    <div
      className={`memory-skill-state is-${tone}`}
      role={tone === "loading" ? "status" : "alert"}
    >
      <span>{children}</span>
      {onRetry && retryLabel ? (
        <Button size="small" aria-label={retryLabel} onClick={onRetry}>
          {retryLabel}
        </Button>
      ) : null}
    </div>
  );
}
