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
  return (
    <details className="paper-guide-generation">
      <summary>{zh ? "生成详情" : "Generation details"}</summary>
      <div>
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
            {revision.coverage.warnings.length > 0 && (
              <span>{revision.coverage.warnings.join(" · ")}</span>
            )}
          </p>
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
