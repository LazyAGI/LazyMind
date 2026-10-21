import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Checkbox, Empty, Tag } from "antd";
import {
  listSkillOrganizeApprovals,
  resolveSkillOrganizeApprovals,
  type SkillOrganizeApprovalItem,
  type SkillOrganizeApprovalTask,
} from "../../skillApi";
import "./skillDraftReview.scss";

interface Props {
  t: (key: string, options?: Record<string, unknown>) => string;
  hiddenRequestId?: string;
  expanded: boolean;
  refreshToken: number;
  onExpandedChange: (expanded: boolean) => void;
  onApplied?: () => void | Promise<void>;
}

const typeLabel = (type: string) => {
  if (type === "merge") return "admin.memorySkillOrganizeApprovalMerge";
  if (type === "delete_duplicate") return "admin.memorySkillOrganizeApprovalDelete";
  return "admin.memorySkillOrganizeApprovalRefactor";
};

export default function SkillOrganizeApprovalPanel({
  t,
  hiddenRequestId = "",
  expanded,
  refreshToken,
  onExpandedChange,
  onApplied,
}: Props) {
  const [tasks, setTasks] = useState<SkillOrganizeApprovalTask[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [selectedPending, setSelectedPending] = useState<Set<string>>(new Set());
  const [selectedAccepted, setSelectedAccepted] = useState<Set<string>>(new Set());

  const reload = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const next = await listSkillOrganizeApprovals();
      setTasks(next);
      const visibleIds = new Set(next.flatMap((task) => task.requestId === hiddenRequestId ? [] : task.items.map((item) => `${task.requestId}:${item.id}`)));
      setSelectedPending((previous) => new Set([...previous].filter((id) => visibleIds.has(id))));
      setSelectedAccepted((previous) => new Set([...previous].filter((id) => visibleIds.has(id))));
    } catch {
      setError("admin.memorySkillOrganizeApprovalRequestFailed");
    } finally {
      setLoading(false);
    }
  }, [hiddenRequestId]);

  useEffect(() => {
    void reload();
  }, [reload, refreshToken]);

  const visible = tasks
    .map((task) => ({ ...task, items: task.requestId === hiddenRequestId ? [] : task.items }))
    .filter((task) => task.items.length > 0 || task.error);
  const pending = visible.flatMap((task) => task.items.filter((item) => item.status !== "accepted").map((item) => ({ task, item })));
  const accepted = visible.flatMap((task) => task.items.filter((item) => item.status === "accepted").map((item) => ({ task, item })));
  const pendingKey = (task: SkillOrganizeApprovalTask, item: SkillOrganizeApprovalItem) => `${task.requestId}:${item.id}`;

  const toggle = (bucket: "pending" | "accepted", key: string, checked: boolean) => {
    const update = bucket === "pending" ? setSelectedPending : setSelectedAccepted;
    update((previous) => {
      const next = new Set(previous);
      if (checked) next.add(key);
      else next.delete(key);
      return next;
    });
  };

  const resolve = async (action: "accept" | "reject" | "revoke", keys: string[]) => {
    if (busy || !keys.length) return;
    setBusy(true);
    setNotice("");
    setError("");
    const groups = new Map<string, string[]>();
    keys.forEach((key) => {
      const separator = key.indexOf(":");
      const requestId = key.slice(0, separator);
      const itemId = key.slice(separator + 1);
      groups.set(requestId, [...(groups.get(requestId) ?? []), itemId]);
    });
    let succeeded = 0;
    let failed = 0;
    try {
      for (const [requestId, itemIds] of groups) {
        const results = await resolveSkillOrganizeApprovals(requestId, itemIds, action);
        results.forEach((result) => {
          if (result.status === "failed" || result.error) failed += 1;
          else succeeded += 1;
        });
        if (results.some((result) => result.error)) {
          setError(results.find((result) => result.error)?.error || "admin.memorySkillOrganizeApprovalRequestFailed");
        }
      }
      setNotice(t("admin.memorySkillOrganizeApprovalResult", { succeeded, failed }));
      if (succeeded) await onApplied?.();
      await reload();
    } catch {
      setError("admin.memorySkillOrganizeApprovalRequestFailed");
    } finally {
      setBusy(false);
    }
  };

  if (!expanded) {
    if (loading || (!pending.length && !accepted.length)) return null;
    return (
      <div className="skill-draft-review__actions">
        <Button type="primary" onClick={() => onExpandedChange(true)}>
          {t("admin.memorySkillOrganizeApprovalOpen", { count: pending.length })}
        </Button>
      </div>
    );
  }

  const renderItem = (task: SkillOrganizeApprovalTask, item: SkillOrganizeApprovalItem, bucket: "pending" | "accepted") => {
    const key = pendingKey(task, item);
    const selected = bucket === "pending" ? selectedPending : selectedAccepted;
    return (
      <article key={key} className="skill-draft-review__package">
        <header>
          <Checkbox
            aria-label={item.targetName || item.sourceKeys[0] || item.id}
            checked={selected.has(key)}
            disabled={busy}
            onChange={(event) => toggle(bucket, key, event.target.checked)}
          >
            {item.targetName || item.sourceKeys[0] || item.id}
          </Checkbox>
          <Tag>{t(typeLabel(item.type))}</Tag>
          {item.deleteKeys.length ? <span>{t("admin.memorySkillOrganizeApprovalSources", { keys: item.deleteKeys.join("、") })}</span> : null}
          {item.dependsOn.length ? <span>{t("admin.memorySkillOrganizeApprovalDepends", { ids: item.dependsOn.join("、") })}</span> : null}
        </header>
        {bucket === "pending" && item.content ? <pre className="skill-draft-review__file"><code>{item.content}</code></pre> : null}
      </article>
    );
  };

  return (
    <section className="skill-draft-review" aria-label={t("admin.memorySkillOrganizeApprovalTitle")}>
      <header className="skill-draft-review__header">
        <div>
          <h3>{t("admin.memorySkillOrganizeApprovalTitle")}</h3>
          <p>{t("admin.memorySkillOrganizeApprovalDescription")}</p>
        </div>
        <Button onClick={() => onExpandedChange(false)} disabled={busy}>{t("common.close")}</Button>
      </header>
      <div className="skill-draft-review__actions">
        <Checkbox
          disabled={busy || !pending.length}
          checked={pending.length > 0 && pending.every(({ task, item }) => selectedPending.has(pendingKey(task, item)))}
          indeterminate={selectedPending.size > 0 && selectedPending.size < pending.length}
          onChange={(event) => setSelectedPending(event.target.checked ? new Set(pending.map(({ task, item }) => pendingKey(task, item))) : new Set())}
        >
          {t("admin.memorySkillOrganizeApprovalSelectAll")}
        </Checkbox>
        <Button onClick={() => void reload()} disabled={loading || busy}>{t("admin.memorySkillOrganizeApprovalReload")}</Button>
        <Button type="primary" loading={busy} disabled={!selectedPending.size} onClick={() => void resolve("accept", [...selectedPending])}>
          {t("admin.memorySkillOrganizeApprovalAccept", { count: selectedPending.size })}
        </Button>
        <Button danger disabled={busy || !selectedPending.size} onClick={() => void resolve("reject", [...selectedPending])}>
          {t("admin.memorySkillOrganizeApprovalReject", { count: selectedPending.size })}
        </Button>
      </div>
      {error ? <Alert type="error" showIcon message={error.startsWith("admin.") ? t(error) : error} /> : null}
      {notice ? <Alert type={error ? "warning" : "success"} showIcon message={notice} /> : null}
      {loading ? <div className="skill-draft-review__loading">{t("admin.memorySkillDraftReviewLoading")}</div> : null}
      {!loading && !visible.length ? <Empty description={t("admin.memorySkillOrganizeApprovalEmpty")} /> : null}
      {visible.map((task) => (
        <div key={task.requestId}>
          <h4>{t("admin.memorySkillOrganizeApprovalTask", { id: task.requestId })}</h4>
          {task.error ? <Alert type="error" showIcon message={task.error} /> : null}
          {task.items.filter((item) => item.status !== "accepted").map((item) => renderItem(task, item, "pending"))}
        </div>
      ))}
      {accepted.length ? (
        <div>
          <div className="skill-draft-review__actions">
            <Checkbox
              disabled={busy}
              checked={accepted.every(({ task, item }) => selectedAccepted.has(pendingKey(task, item)))}
              indeterminate={selectedAccepted.size > 0 && selectedAccepted.size < accepted.length}
              onChange={(event) => setSelectedAccepted(event.target.checked ? new Set(accepted.map(({ task, item }) => pendingKey(task, item))) : new Set())}
            >
              {t("admin.memorySkillOrganizeApprovalSelectAccepted")}
            </Checkbox>
            <span>{t("admin.memorySkillOrganizeApprovalAccepted")}</span>
            <Button disabled={busy || !selectedAccepted.size} onClick={() => void resolve("revoke", [...selectedAccepted])}>
              {t("admin.memorySkillOrganizeApprovalRevoke", { count: selectedAccepted.size })}
            </Button>
          </div>
          {accepted.map(({ task, item }) => renderItem(task, item, "accepted"))}
        </div>
      ) : null}
    </section>
  );
}
