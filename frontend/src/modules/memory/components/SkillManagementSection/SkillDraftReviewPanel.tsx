import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Empty, Popconfirm, Spin, Tag } from "antd";
import { mapSkillDiffEntryLines } from "../skillPackage/skillDiffUtils";
import {
  applySkillDraftBatch, draftReviewError, listPendingSkillDrafts, loadSkillDraftReview,
  rejectSkillDraft, type PendingSkillDraft, type SkillDraftReview,
} from "./skillDraftReview";
import "./skillDraftReview.scss";

interface Props {
  t: (key: string, options?: Record<string, unknown>) => string;
  onClose: () => void;
  onApplied: () => void | Promise<void>;
  onPendingCountChange?: (count: number) => void;
  onOpenSkill?: (skillId: string) => void;
  onApplyingChange?: (applying: boolean) => void;
}
interface ReviewRow { pending: PendingSkillDraft; preview?: SkillDraftReview; error?: string }

export default function SkillDraftReviewPanel({t, onClose, onApplied, onPendingCountChange, onOpenSkill, onApplyingChange}: Props) {
  const [rows, setRows] = useState<ReviewRow[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState("");
  const [failures, setFailures] = useState<Record<string, string>>({});
  const [appliedCount, setAppliedCount] = useState<number | null>(null);
  const [rejectingId, setRejectingId] = useState("");
  const generation = useRef(0);
  const alive = useRef(false);
  const busy = useRef(false);
  const callbacks = useRef({onApplied, onPendingCountChange, onApplyingChange});
  callbacks.current = {onApplied, onPendingCountChange, onApplyingChange};
  const translateError = (value: string) => value.startsWith("admin.") ? t(value) : value;

  const reload = useCallback(async () => {
    const request = ++generation.current;
    const current = () => alive.current && generation.current === request;
    setLoading(true);
    setError("");
    try {
      const pending = await listPendingSkillDrafts();
      if (!current()) return;
      callbacks.current.onPendingCountChange?.(pending.length);
      setRows(pending.map(item => ({pending: item})));
      setSelected(previous => new Set([...previous].filter(id => pending.some(row => row.skill.skillId === id))));
      for (let offset = 0; offset < pending.length; offset += 4) {
        if (!current()) return;
        const previews = await Promise.all(pending.slice(offset, offset + 4).map(async item => {
          try { return {pending: item, preview: await loadSkillDraftReview(item)}; }
          catch (cause) { return {pending: item, error: draftReviewError(cause)}; }
        }));
        if (!current()) return;
        setRows(previous => previous.map(row => previews.find(item => item.pending.skill.skillId === row.pending.skill.skillId) ?? row));
      }
    } catch (cause) {
      if (current()) setError(draftReviewError(cause));
    } finally { if (current()) setLoading(false); }
  }, []);

  useEffect(() => {
    alive.current = true;
    void reload();
    return () => {
      alive.current = false;
      generation.current++;
      callbacks.current.onApplyingChange?.(false);
    };
  }, [reload]);

  const eligible = rows.filter(row => row.preview);
  const chosen = eligible.filter(row => selected.has(row.pending.skill.skillId));
  const apply = async () => {
    if (busy.current || loading || error || !chosen.length) return;
    busy.current = true;
    setApplying(true);
    callbacks.current.onApplyingChange?.(true);
    setFailures({});
    setAppliedCount(null);
    try {
      const result = await applySkillDraftBatch(chosen.map(row => row.preview!), () => alive.current);
      if (!alive.current) return;
      setFailures(result.failed);
      setAppliedCount(result.succeeded.length);
      setSelected(new Set(Object.keys(result.failed)));
      if (result.succeeded.length) {
        await reload();
        if (alive.current) await callbacks.current.onApplied();
      }
    } catch (cause) { if (alive.current) setError(draftReviewError(cause)); }
    finally {
      busy.current = false;
      if (alive.current) {
        setApplying(false);
        callbacks.current.onApplyingChange?.(false);
      }
    }
  };

  const rejectOne = async (row: ReviewRow) => {
    const id = row.pending.skill.skillId;
    if (busy.current || loading) return;
    busy.current = true;
    setApplying(true);
    setRejectingId(id);
    callbacks.current.onApplyingChange?.(true);
    setFailures({});
    try {
      await rejectSkillDraft(row.pending);
      if (!alive.current) return;
      setSelected((previous) => {
        const next = new Set(previous);
        next.delete(id);
        return next;
      });
      await reload();
      if (alive.current) await callbacks.current.onApplied();
    } catch (cause) {
      if (alive.current) setFailures({[id]: draftReviewError(cause)});
    } finally {
      busy.current = false;
      if (alive.current) {
        setApplying(false);
        setRejectingId("");
        callbacks.current.onApplyingChange?.(false);
      }
    }
  };

  return <section className="skill-draft-review" aria-label={t("admin.memorySkillDraftReviewTitle")}>
    <header className="skill-draft-review__header">
      <div><h3>{t("admin.memorySkillDraftReviewTitle")}</h3><p>{t("admin.memorySkillDraftReviewDescription")}</p></div>
      <Button onClick={onClose} disabled={applying}>{t("common.close")}</Button>
    </header>
    <div className="skill-draft-review__actions">
      <Checkbox disabled={loading || applying || Boolean(error) || !eligible.length}
        checked={eligible.length > 0 && chosen.length === eligible.length}
        indeterminate={chosen.length > 0 && chosen.length < eligible.length}
        onChange={event => setSelected(event.target.checked ? new Set(eligible.map(row => row.pending.skill.skillId)) : new Set())}>
        {t("admin.memorySkillDraftReviewSelectAll")}
      </Checkbox>
      <span>{t("admin.memorySkillDraftReviewSelection", {selected: chosen.length, total: rows.length})}</span>
      <Button onClick={() => void reload()} disabled={loading || applying}>{t("admin.memorySkillDraftReviewReload")}</Button>
      <Button type="primary" loading={applying} disabled={loading || Boolean(error) || !chosen.length} onClick={() => void apply()}>
        {t("admin.memorySkillDraftReviewApply", {count: chosen.length})}
      </Button>
    </div>
    {error && <Alert type="error" showIcon message={translateError(error)} />}
    {appliedCount !== null && <Alert type={Object.keys(failures).length ? "warning" : "success"} showIcon
      message={t("admin.memorySkillDraftReviewResult", {succeeded: appliedCount, failed: Object.keys(failures).length})} />}
    {loading && <div className="skill-draft-review__loading"><Spin size="small" /> {t("admin.memorySkillDraftReviewLoading")}</div>}
    {!loading && !error && !rows.length && <Empty description={t("admin.memorySkillDraftReviewEmpty")} />}
    <div className="skill-draft-review__packages">
      {rows.map(row => {
        const id = row.pending.skill.skillId;
        return <article key={id} className="skill-draft-review__package">
          <header>
            <Checkbox aria-label={row.pending.skill.name} disabled={!row.preview || loading || applying || Boolean(error)}
              checked={selected.has(id)} onChange={event => setSelected(previous => {
                const next = new Set(previous);
                if (event.target.checked) next.add(id); else next.delete(id);
                return next;
              })}>{row.pending.skill.name}</Checkbox>
            <Tag>{t("admin.memorySkillDraftReviewPackage")}</Tag>
            <span>{row.pending.skill.category}</span>
            <Popconfirm
              title={t("admin.memorySkillDraftReviewRejectConfirmTitle")}
              description={t("admin.memorySkillDraftReviewRejectConfirmContent")}
              okText={t("admin.memorySkillDraftReviewRejectConfirmOk")}
              cancelText={t("common.cancel")}
              okButtonProps={{danger: true}}
              disabled={loading || applying}
              onConfirm={() => void rejectOne(row)}
            >
              <Button size="small" danger disabled={loading || applying} loading={rejectingId === id}>
                {t("admin.memorySkillDraftReviewReject")}
              </Button>
            </Popconfirm>
          </header>
          {failures[id] && <Alert type="error" message={translateError(failures[id])} />}
          {row.error && <Alert type="error" message={translateError(row.error)}
            action={onOpenSkill && <Button size="small" disabled={applying} onClick={() => onOpenSkill(id)}>{t("admin.memoryCloudViewDetail")}</Button>} />}
          {row.preview?.files.map(file => <div className="skill-draft-review__file" key={file.path}>
            <div className="skill-draft-review__file-title"><code>{file.path}</code><Tag>{t(`admin.memorySkillDraftReviewFile${file.status.charAt(0).toUpperCase()}${file.status.slice(1)}`)}</Tag></div>
            <pre aria-label={file.path}>{mapSkillDiffEntryLines(file.diffEntryLines).map((line, index) =>
              <div key={index} className={`skill-draft-review__line skill-draft-review__line--${line.type}`}>
                <span aria-hidden="true">{line.type === "add" ? "+" : line.type === "remove" ? "−" : " "}</span><code>{line.text}</code>
              </div>)}{!file.diffEntryLines.length && <span>{t("admin.memorySkillDraftReviewFileStructure")}</span>}</pre>
          </div>)}
        </article>;
      })}
    </div>
  </section>;
}
