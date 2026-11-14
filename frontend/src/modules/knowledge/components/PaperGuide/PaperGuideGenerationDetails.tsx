import { coverageWarning } from "./coverageWarning";
import { useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import type { PaperGuideRevisionResponse } from "@/api/generated/core-client";
import type { ReadingProviderSelection } from "../../api/readingPreparation";
export default function PaperGuideGenerationDetails({
  zh,
  model,
  setModel,
  models,
  revision,
  busy,
  canGenerate,
  active,
  onRegenerate,
}: {
  zh: boolean;
  model: string;
  setModel: (value: string) => void;
  models?: ReadingProviderSelection;
  revision?: PaperGuideRevisionResponse;
  busy: boolean;
  canGenerate: boolean;
  active: boolean;
  onRegenerate: () => void;
}) {
  const details = useRef<HTMLDetailsElement>(null);
  const [open, setOpen] = useState(false);
  const [bounds, setBounds] = useState<CSSProperties>({});
  useLayoutEffect(() => {
    if (!open) return;
    const toolbar = details.current?.closest<HTMLElement>(
      ".paper-guide-toolbar",
    );
    const workspace = details.current?.closest<HTMLElement>(
      ".paper-reading-workspace",
    );
    if (!toolbar || !workspace) return;
    const fit = () => {
      const bar = toolbar.getBoundingClientRect();
      const bottom = Math.min(
        window.innerHeight,
        workspace.getBoundingClientRect().bottom,
      );
      // At high zoom, overlay the toolbar rather than place the actions below
      // the visible reader. The panel itself scrolls; no actions are removed.
      const top = bottom - bar.bottom >= 160 ? bar.height : 0;
      setBounds({ top, maxHeight: Math.max(0, bottom - bar.top - top - 8) });
    };
    fit();
    const observer = new ResizeObserver(fit);
    observer.observe(toolbar);
    observer.observe(workspace);
    window.addEventListener("resize", fit);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", fit);
    };
  }, [open]);
  const close = () => {
    if (!details.current) return;
    details.current.open = false;
    setOpen(false);
    details.current.querySelector("summary")?.focus();
  };
  return (
    <details
      ref={details}
      className="paper-guide-generation"
      onToggle={(event) => setOpen(event.currentTarget.open)}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.stopPropagation();
          close();
        }
      }}
    >
      <summary>{zh ? "生成详情" : "Generation details"}</summary>
      <div style={bounds}>
        <button className="paper-guide-generation-close" onClick={close}>
          {zh ? "关闭详情" : "Close details"}
        </button>
        <label>
          {zh ? "本次研读模型" : "Model for this run"}
          <select value={model} onChange={(e) => setModel(e.target.value)}>
            <option value="">
              {zh ? "跟随阅读偏好" : "Use reading preference"}
            </option>
            {models?.options.map((m) => (
              <option key={m.id} value={m.id}>
                {m.label}
              </option>
            ))}
          </select>
        </label>
        {revision && (
          <p>
            {revision.model.name} ·{" "}
            {new Date(revision.created_at).toLocaleString()}
            <br />
            {zh ? "费用用量未知" : "Cost usage unavailable"}
            <br />
            {zh ? "来源版本" : "Source version"}:{" "}
            {revision.source_sha256.slice(0, 12)}
            <br />
            {zh ? "已发出模型请求" : "Model requests sent"}:{" "}
            {revision.run.calls_used} / {revision.run.call_limit}
            <br />
            {zh ? "正文覆盖" : "Body covered"}:{" "}
            {revision.coverage.body ? (zh ? "是" : "Yes") : zh ? "否" : "No"}
          </p>
        )}
        {!!revision?.coverage.warnings.length && (
          <ul className="paper-guide-coverage-warnings">
            {revision.coverage.warnings.map((warning, index) => (
              <li key={index}>{coverageWarning(warning, zh)}</li>
            ))}
          </ul>
        )}
        <button
          disabled={busy || !canGenerate || active}
          onClick={onRegenerate}
        >
          {zh ? "重新生成" : "Regenerate"}
        </button>
      </div>
    </details>
  );
}
