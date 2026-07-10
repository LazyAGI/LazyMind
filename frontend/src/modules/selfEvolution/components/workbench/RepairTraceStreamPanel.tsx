import { useEffect, useMemo, useRef } from "react";
import { Typography } from "antd";
import { useTranslation } from "react-i18next";
import {
  buildRepairTraceAttemptGroups,
  type RepairTraceRow,
} from "../../shared/repairTrace";

const { Text } = Typography;

function RepairTraceRowItem({ row }: { row: RepairTraceRow }) {
  return (
    <div className={`self-evolution-repair-trace-row is-${row.action}`}>
      <span className={`self-evolution-repair-trace-dot is-${row.action}`} />
      <div className="self-evolution-repair-trace-content">
        <div className="self-evolution-repair-trace-row-head">
          <span className="self-evolution-repair-trace-category">{row.title}</span>
          <span className={`self-evolution-repair-trace-status is-${row.action}`}>
            {row.statusLabel}
          </span>
        </div>
        {(row.caseId || row.chips.length > 0 || row.detail) && (
          <div className="self-evolution-repair-trace-row-body">
            {row.caseId ? (
              <span className="self-evolution-repair-trace-case">{row.caseId}</span>
            ) : null}
            {row.chips.map((chip) => (
              <span
                key={`${row.key}-${chip}`}
                className="self-evolution-repair-trace-chip"
              >
                {chip}
              </span>
            ))}
            {row.detail ? (
              <Text className="self-evolution-repair-trace-detail">{row.detail}</Text>
            ) : null}
          </div>
        )}
      </div>
    </div>
  );
}

export function RepairTraceStreamPanel({ rows }: { rows: RepairTraceRow[] }) {
  const { t } = useTranslation();
  const listRef = useRef<HTMLDivElement | null>(null);
  const attemptGroups = useMemo(() => buildRepairTraceAttemptGroups(rows), [rows]);
  const runningCount = rows.filter((row) => row.action === "running").length;
  const doneCount = rows.filter((row) => row.action === "done").length;
  const failedCount = rows.filter((row) => row.action === "failed").length;
  const activeAttempt =
    attemptGroups.find((group) => group.status === "running")?.attempt ||
    attemptGroups[attemptGroups.length - 1]?.attempt;

  useEffect(() => {
    const container = listRef.current;
    if (!container || rows.length === 0) {
      return;
    }
    container.scrollTop = container.scrollHeight;
  }, [rows.length, rows[rows.length - 1]?.key]);

  return (
    <section
      className="self-evolution-repair-trace"
      aria-label={t("selfEvolutionRun.repairTraceAria")}
    >
      <div className="self-evolution-repair-trace-head">
        <Text>{t("selfEvolutionRun.repairTraceTitle")}</Text>
        <Text>
          {rows.length > 0
            ? attemptGroups.length > 1
              ? t("selfEvolutionRun.repairTraceAttemptOverview", {
                  attemptCount: attemptGroups.length,
                  activeAttempt,
                })
              : runningCount > 0
                ? t("selfEvolutionRun.repairTraceProgress", {
                    done: doneCount,
                    total: rows.length,
                    running: runningCount,
                  })
                : failedCount > 0
                  ? t("selfEvolutionRun.repairTraceProgressFailed", {
                      failed: failedCount,
                      total: rows.length,
                    })
                  : t("selfEvolutionRun.repairTraceProgressDone", {
                      done: doneCount,
                      total: rows.length,
                    })
            : t("selfEvolutionRun.repairTraceWaiting")}
        </Text>
      </div>

      <div
        ref={listRef}
        className="self-evolution-repair-trace-list"
        aria-label={t("selfEvolutionRun.repairTraceListAria")}
      >
        {attemptGroups.length === 0 ? (
          <Text className="self-evolution-repair-trace-empty">
            {t("selfEvolutionRun.repairTraceEmpty")}
          </Text>
        ) : (
          attemptGroups.map((group) => (
            <section
              key={group.key}
              className={`self-evolution-repair-trace-attempt is-${group.status}`}
              aria-label={group.label}
            >
              <div className="self-evolution-repair-trace-attempt-head">
                <Text className="self-evolution-repair-trace-attempt-title">
                  {group.label}
                </Text>
                <span
                  className={`self-evolution-repair-trace-attempt-status is-${group.status}`}
                >
                  {group.statusLabel}
                </span>
              </div>

              {group.phaseSummaries.length > 0 ? (
                <div
                  className="self-evolution-repair-trace-phases"
                  aria-label={t("selfEvolutionRun.repairTracePhasesAria")}
                >
                  {group.phaseSummaries.map((phase) => (
                    <div
                      key={`${group.key}-${phase.category}`}
                      className={`self-evolution-repair-trace-phase is-${phase.status}`}
                    >
                      <span className="self-evolution-repair-trace-phase-label">
                        {phase.label}
                      </span>
                      <span className="self-evolution-repair-trace-phase-meta">
                        {phase.count > 0
                          ? t("selfEvolutionRun.repairTracePhaseCount", {
                              count: phase.count,
                            })
                          : null}
                      </span>
                      <span
                        className={`self-evolution-repair-trace-phase-status is-${phase.status}`}
                      >
                        {phase.statusLabel}
                      </span>
                    </div>
                  ))}
                </div>
              ) : null}

              <div className="self-evolution-repair-trace-attempt-rows">
                {group.rows.map((row) => (
                  <RepairTraceRowItem key={row.key} row={row} />
                ))}
              </div>
            </section>
          ))
        )}
      </div>
    </section>
  );
}
