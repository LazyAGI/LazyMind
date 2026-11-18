import { useEffect, useState } from "react";

export function GuideSkeleton() {
  return <div className="guide-skeleton" aria-hidden="true"><span className="guide-skeleton-heading"/>{[0, 1, 2, 3].map(i => <span key={i}/>)}<div className="guide-skeleton-figure"/>{[4, 5, 6].map(i => <span key={i}/>)}</div>;
}

export default function GuideGenerationProgress({ stage, total = 0, done = 0, writing = [], zh, compact = false, startedAt, generationStartedAt: knownGenerationStart }: { generationStartedAt?: number; startedAt?: number; compact?: boolean; stage: string; total?: number; done?: number; writing?: string[]; zh: boolean }) {
  const [mountedAt] = useState(Date.now);
  const started = startedAt ?? mountedAt;
  const [elapsed, setElapsed] = useState(() => Math.floor((Date.now() - started) / 1000));
  useEffect(() => {
    const timer = window.setInterval(() => setElapsed(Math.floor((Date.now() - started) / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [started]);
  const waiting = stage === "queued" || stage === "submitting";
  const [generationStartedAt, setGenerationStartedAt] = useState<number | null>(() => waiting ? null : started);
  useEffect(() => {
    if (waiting) setGenerationStartedAt(null);
    else setGenerationStartedAt(value => value ?? Date.now());
  }, [waiting]);
  const activeStart = knownGenerationStart ?? generationStartedAt;
  const generationElapsed = activeStart === null ? 0 : Math.max(0, Math.floor((Date.now() - activeStart) / 1000));
  const estimate = waiting
    ? (zh ? "开始后约 2 分钟" : "About 2 min once started")
    : generationElapsed < 120 ? (zh ? "预计约 2 分钟" : "About 2 min") : (zh ? "仍在生成" : "Still generating");
  const step = stage === "checking" ? 3 : stage === "writing" ? 2 : stage === "planning" ? 1 : 0;
  const labels = zh ? ["读取论文", "规划目录", "生成章节", "整理导读"] : ["Read paper", "Plan outline", "Write chapters", "Finish guide"];
  const status = stage === "queued" ? (zh ? "排队中" : "Queued") : stage === "submitting" ? (zh ? "正在提交" : "Starting") : labels[step];
  if (compact) return <span className="guide-progress-compact">
    <span className={`guide-progress-track${total ? "" : " is-indeterminate"}`} aria-hidden="true">
      <i style={total ? { transform: `scaleX(${Math.min(1, done / total)})` } : undefined} />
    </span>
    <span role="status">{status}{total > 0 && ` · ${done}/${total}`}</span>
    <span className="guide-progress-estimate">{estimate}</span>
    <span className="guide-progress-elapsed">{Math.floor(elapsed / 60)}:{String(elapsed % 60).padStart(2, "0")}</span>
  </span>;
  return <div className="guide-generation-progress">
    <div className="guide-generation-heading" role="status" aria-live="polite"><strong>{status}</strong><span>{total > 0 ? (zh ? `${done} / ${total} 章可阅读` : `${done} / ${total} chapters ready`) : stage === "writing" ? (zh ? "新版章节正在生成" : "New chapters are being written") : stage === "checking" ? (zh ? "正在整理已生成的章节" : "Preparing the completed chapters") : (zh ? "目录就绪后会立即显示" : "The outline will appear as soon as it is ready")}</span></div>
    <ol aria-label={zh ? "生成步骤" : "Generation steps"}>{labels.map((label, i) => <li key={label} data-state={waiting ? "queued" : i < step ? "done" : i === step ? "active" : "queued"} aria-current={!waiting && i === step ? "step" : undefined}><span aria-hidden="true">{i + 1}</span>{label}</li>)}</ol>
    {total > 0 && <progress max={total} value={done} aria-label={zh ? "已完成章节" : "Completed chapters"}/>}
    {stage === "writing" && total > 0 && <div className="guide-chapter-activity">
      <span>{zh ? `生成中 ${writing.length} · 排队中 ${Math.max(0, total - done - writing.length)}` : `Writing ${writing.length} · Queued ${Math.max(0, total - done - writing.length)}`}</span>
      {writing.length > 0 && <ul>{writing.map((title, i) => <li key={i}>{title}</li>)}</ul>}
    </div>}
    <p>{stage === "submitting" ? (zh ? "正在提交生成请求，任务开始后预计约 2 分钟。" : "Submitting your request; generation usually takes about 2 minutes after it starts.") : waiting ? (zh ? "正在等待可用任务位，开始生成后约 2 分钟；排队时间另计。" : "Waiting for an available worker; generation usually takes about 2 minutes after it starts.") : generationElapsed < 120 ? (zh ? "预计约 2 分钟，实际时间随论文长度和模型速度变化。" : "Usually about 2 minutes; timing varies by paper and model.") : (zh ? "比预计时间稍久，任务仍在进行；完成的章节会陆续出现。" : "Taking longer than expected. Completed chapters will continue to appear.")} <span className="guide-elapsed">{zh ? `已等待 ${Math.floor(elapsed / 60)}:${String(elapsed % 60).padStart(2, "0")}` : `Elapsed ${Math.floor(elapsed / 60)}:${String(elapsed % 60).padStart(2, "0")}`}</span></p>
    <p>{zh ? "可以先读已完成章节，或切回原文，生成会继续。" : "Read ready chapters or return to the paper while generation continues."}</p>
  </div>;
}
