import { Button, message, Progress } from "antd";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import "./index.scss";

export interface ToolLimitPending {
  decision_id: string;
  used_rounds: number;
  round_limit: number;
  expanded_max_rounds: number;
  timeout_seconds: number;
}

interface ToolLimitCardProps {
  pending: ToolLimitPending;
  onDecision: (action: "continue" | "summarize") => Promise<void>;
}

export default function ToolLimitCard({ pending, onDecision }: ToolLimitCardProps) {
  const { t } = useTranslation();
  const totalSeconds = Math.max(0, Math.ceil(pending.timeout_seconds || 0));
  const [remaining, setRemaining] = useState(totalSeconds);
  const [resolved, setResolved] = useState<"continue" | "summarize" | "auto" | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    setRemaining(totalSeconds);
    setResolved(null);
    setSubmitting(false);
  }, [pending.decision_id, totalSeconds]);

  useEffect(() => {
    if (resolved || totalSeconds <= 0) {
      if (!resolved && totalSeconds <= 0) setResolved("auto");
      return;
    }
    const timer = window.setInterval(() => {
      setRemaining((value) => {
        if (value <= 1) {
          window.clearInterval(timer);
          setResolved("auto");
          return 0;
        }
        return value - 1;
      });
    }, 1000);
    return () => window.clearInterval(timer);
  }, [resolved, totalSeconds]);

  const percent = useMemo(
    () => totalSeconds > 0 ? Math.round((remaining / totalSeconds) * 100) : 0,
    [remaining, totalSeconds],
  );

  const choose = async (action: "continue" | "summarize") => {
    if (resolved || submitting) return;
    setSubmitting(true);
    try {
      await onDecision(action);
      setResolved(action);
    } catch {
      message.error(t("chat.toolLimitDecisionFailed"));
    } finally {
      setSubmitting(false);
    }
  };

  if (resolved === "continue" || resolved === "summarize") return null;

  return (
    <div className="tool-limit-card">
      <div className="tool-limit-card__title">{t("chat.toolLimitTitle")}</div>
      <p className="tool-limit-card__description">
        {t("chat.toolLimitDescription", {
          used: pending.used_rounds,
          max: pending.expanded_max_rounds,
        })}
      </p>
      {resolved ? (
        <div className="tool-limit-card__status">
          {resolved === "summarize"
            ? t("chat.toolLimitSummarizing")
            : resolved === "auto"
              ? t("chat.toolLimitAutoContinued")
              : t("chat.toolLimitContinuing")}
        </div>
      ) : (
        <>
          <Progress percent={percent} showInfo={false} size={["100%", 4]} />
          <div className="tool-limit-card__countdown">
            {t("chat.toolLimitCountdown", { seconds: remaining })}
          </div>
          <div className="tool-limit-card__actions">
            <Button type="primary" loading={submitting} onClick={() => choose("continue")}>
              {t("chat.toolLimitContinue")}
            </Button>
            <Button disabled={submitting} onClick={() => choose("summarize")}>
              {t("chat.toolLimitSummarize")}
            </Button>
          </div>
        </>
      )}
    </div>
  );
}
