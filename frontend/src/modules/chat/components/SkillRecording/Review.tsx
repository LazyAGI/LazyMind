import { useEffect, useState } from "react";
import { Alert, Button, Space } from "antd";
import { useTranslation } from "react-i18next";
import { decideRecording, listRecordings, type SkillRecording } from "./api";

export default function RecordingReview({ skillId, onDecision }: { skillId: string; onDecision: (keep: boolean) => void | Promise<void> }) {
  const { t } = useTranslation();
  const [row, setRow] = useState<SkillRecording>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    setRow(undefined);
    const load = () => listRecordings({ skill_id: skillId }).then((rows) => { if (active) setRow(rows.find((item) => item.status === "pending")); }).catch(() => { if (active) setError(t("recording.loadFailed")); });
    void load(); window.addEventListener("focus", load);
    return () => { active = false; window.removeEventListener("focus", load); };
  }, [skillId, t]);
  const decide = async (keep: boolean) => {
    if (!row || busy) return;
    setBusy(true); setError("");
    try { await decideRecording(row.id, keep); setRow(undefined); await onDecision(keep); }
    catch (e) { setError(e instanceof Error ? e.message : t("recording.failed")); }
    finally { setBusy(false); }
  };
  return <>
    {error && <Alert type="error" message={error} />}
    {row && <Alert type="warning" showIcon message={t("recording.status.pending")} description={<><p>{t("recording.reviewHint")}</p><Space><Button type="primary" loading={busy} onClick={() => void decide(true)}>{t("recording.keep")}</Button><Button danger disabled={busy} onClick={() => void decide(false)}>{t("recording.discard")}</Button></Space></>} />}
  </>;
}
