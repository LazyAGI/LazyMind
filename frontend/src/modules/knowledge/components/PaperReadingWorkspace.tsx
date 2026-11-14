import { Modal } from "antd";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";
import {
  extractErrorCode,
  getLocalizedErrorMessage,
} from "@/components/request";
import type {
  PaperGuideEvidence,
  PaperGuideEvidenceResponse,
  PaperGuideProgressRequest,
  PaperGuideBlock,
} from "@/api/generated/core-client";
import type { DocumentChatSelection } from "./PdfTemporaryChat/types";
import {
  getReadingPreferences,
  type ReadingProviderSelection,
} from "../api/readingPreparation";
import { paperGuideApi } from "../api/paperGuide";
import { usePaperGuide } from "./PaperGuide/usePaperGuide";
import PaperGuideReader from "./PaperGuide/PaperGuideReader";
import PaperGuideEvidencePreview from "./PaperGuide/PaperGuideEvidence";
import PaperGuideAsset, {
  type GuidePreview,
} from "./PaperGuide/PaperGuideAsset";
import { useGuideSourcePreview } from "./PaperGuide/useGuideSourcePreview";
import PaperGuideGenerationDetails from "./PaperGuide/PaperGuideGenerationDetails";
import type { PdfIRDocument } from "../api/pdfIR";
interface Props {
  dataset: string;
  document: string;
  title: string;
  eligible: boolean;
  source: ReactNode;
  auxiliary: ReactNode;
  dock: ReactNode;
  divider?: ReactNode;
  back: ReactNode;
  auxiliaryOpen: boolean;
  auxiliaryTab: string;
  onAuxiliary: (tab: string | undefined) => void;
  onSelection: (s: DocumentChatSelection) => void;
  onNavigate: (e: PaperGuideEvidence) => void;
  onReturnSource?: () => void;
  getPdfData: () => ArrayBuffer | null;
  ir?: PdfIRDocument;
  onModeChange?: (guide: boolean) => void;
}
const activeStages = [
  "queued",
  "waiting_source",
  "analyzing",
  "writing",
  "reviewing",
];
export default function PaperReadingWorkspace(p: Props) {
  const { i18n } = useTranslation();
  const language = i18n.language.startsWith("zh") ? "zh" : "en",
    zh = language === "zh";
  const guide = usePaperGuide(p.dataset, p.document, language, p.eligible);
  const storageKey = `paper-guide-mode/${guide.identity}/${p.dataset}/${p.document}`;
  const [mode, setMode] = useState("source");
  const [sourceOpen, setSourceOpen] = useState(false);
  const [width, setWidth] = useState(0);
  const container = useRef<HTMLDivElement>(null);
  const [metadata, setMetadata] = useState<{
    title: string;
    authors: string[];
    year: number;
  }>();
  const [model, setModel] = useState("");
  const [models, setModels] = useState<ReadingProviderSelection>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const candidate = guide.status?.candidate_revision || guide.revision;
  const mutationScope = `${storageKey}/${language}/${candidate?.id || ""}`;
  const latestMutationScope = useRef(mutationScope);
  latestMutationScope.current = mutationScope;
  const mutationSequence = useRef(0);
  const [rejectedResume, setRejectedResume] = useState<string>();
  const requiresRegeneration =
    candidate?.run.error_code === "paper_guide_regeneration_required" ||
    rejectedResume === mutationScope;
  useEffect(() => {
    mutationSequence.current += 1;
    setRejectedResume(undefined);
    setError("");
    setBusy(false);
  }, [mutationScope]);
  const [evidence, setEvidence] = useState<PaperGuideEvidenceResponse>();
  const [evidenceLoading, setEvidenceLoading] = useState(false);
  const evidenceAbort = useRef<AbortController>();
  const latest = useRef("");
  latest.current = storageKey;
  const progressTimer = useRef<ReturnType<typeof setTimeout>>();
  const opened = useRef(false);
  const target = useMemo(
    () => ({ dataset: p.dataset, document: p.document, language }),
    [p.dataset, p.document, language],
  );
  const localProgress = useRef<PaperGuideProgressRequest>();
  const canEnter = !!(
    guide.status?.enabled ||
    (guide.status?.can_read &&
      (guide.status.published_revision || guide.status.candidate_revision))
  );
  const showGuide = p.eligible && mode === "article" && canEnter;
  useEffect(() => {
    setMode(localStorage.getItem(storageKey) || "source");
    setSourceOpen(false);
    setEvidence(undefined);
    setEvidenceLoading(false);
    setError("");
    setBusy(false);
    setModel("");
    setModels(undefined);
    setMetadata(undefined);
    localProgress.current = undefined;
    opened.current = false;
    return () => {
      evidenceAbort.current?.abort();
      clearTimeout(progressTimer.current);
    };
  }, [storageKey]);
  useEffect(() => {
    if (guide.status?.published_revision && !localStorage.getItem(storageKey))
      setMode("article");
  }, [guide.status?.published_revision?.id, storageKey]);
  useEffect(() => {
    p.onModeChange?.(showGuide);
  }, [showGuide, p.onModeChange]);
  useEffect(() => {
    if (!container.current) return;
    const observer = new ResizeObserver((entries) =>
      setWidth(entries[0].contentRect.width),
    );
    observer.observe(container.current);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!showGuide || opened.current) return;
    opened.current = true;
    p.onAuxiliary(undefined);
    void getReadingPreferences()
      .then((v) => {
        if (latest.current === storageKey)
          setModels(v.provider_selections?.study);
      })
      .catch(() => {});
  }, [showGuide, storageKey]);
  useEffect(() => {
    if (!showGuide) return;
    const abort = new AbortController();
    void paperGuideApi
      .metadata(target, abort.signal)
      .then((value) => {
        if (!abort.signal.aborted) setMetadata(value);
      })
      .catch(() => {});
    return () => abort.abort();
  }, [showGuide, target]);
  const setReadingMode = (value: string) => {
    setMode(value);
    localStorage.setItem(storageKey, value);
    if (value === "source") setSourceOpen(false);
  };
  const run = async (
    action: "generate" | "regenerate" | "cancel" | "resume",
  ) => {
    const sequence = ++mutationSequence.current;
    const isCurrent = () =>
      latestMutationScope.current === mutationScope &&
      mutationSequence.current === sequence;
    setBusy(true);
    setError("");
    try {
      if (action === "generate" || action === "regenerate") {
        await paperGuideApi.generate(target, {
          language,
          expected_source_hash: guide.status?.source_sha256 || "",
          idempotency_key: crypto.randomUUID(),
          regenerate: action === "regenerate",
          ...(model ? { model_ref: model } : {}),
        });
      } else if (candidate) {
        await paperGuideApi[action](target, candidate.id);
      }
      if (isCurrent()) await guide.refresh();
    } catch (e) {
      if (isCurrent()) {
        if (action === "resume" && extractErrorCode(e) === "2004413") {
          // The response can precede status persistence. Keep this revision
          // blocked even if the following status fetch fails or is stale.
          setRejectedResume(mutationScope);
          void guide.refresh().catch(() => {});
        } else {
          setError(getLocalizedErrorMessage(e));
        }
      }
    } finally {
      if (isCurrent()) setBusy(false);
    }
  };
  const requestEvidence = useCallback(
    async (id: string) => {
      if (!guide.revision) return;
      const key = storageKey;
      evidenceAbort.current?.abort();
      const abort = new AbortController();
      evidenceAbort.current = abort;
      setEvidence(undefined);
      setEvidenceLoading(true);
      try {
        const e = await paperGuideApi.evidence(
          target,
          guide.revision.id,
          id,
          abort.signal,
        );
        if (!abort.signal.aborted && latest.current === key) setEvidence(e);
      } catch (e) {
        if (!abort.signal.aborted && latest.current === key)
          setError(getLocalizedErrorMessage(e));
      } finally {
        if (!abort.signal.aborted && latest.current === key)
          setEvidenceLoading(false);
      }
    },
    [storageKey, target, guide.revision?.id],
  );
  useEffect(() => {
    evidenceAbort.current?.abort();
    setEvidence(undefined);
    setEvidenceLoading(false);
  }, [guide.revision?.id]);
  const preview = useGuideSourcePreview(
    storageKey,
    guide.revision?.source_sha256,
    !!guide.revision?.source_current,
    p.ir?.source_sha256,
    p.getPdfData,
  );
  const saveProgress = (progress: PaperGuideProgressRequest) => {
    localProgress.current = progress;
    clearTimeout(progressTimer.current);
    progressTimer.current = setTimeout(() => {
      void paperGuideApi.progress(target, progress).catch(() => {});
    }, 700);
  };
  const sourceVisible = !showGuide || sourceOpen;
  const auxVisible = p.auxiliaryOpen && !sourceOpen;
  const dual = width >= 1100;
  const stage = candidate?.run.stage;
  const resumable = !!stage && ["paused", "failed", "canceled"].includes(stage);
  const active = !!stage && activeStages.includes(stage);
  const errorNames = [
    "source_changed",
    "preparation_required",
    "model_unavailable",
    "budget_exhausted",
    "context_too_long",
    "invalid_evidence",
    "material_insufficient",
    "permission_revoked",
    "conflict",
    "disabled",
    "review_required",
    "generation_failed",
    "regeneration_required",
  ];
  const reasonText = (value?: string) => {
    if (!value) return "";
    const index = errorNames.indexOf(value.replace(/^paper_guide_/, ""));
    return index >= 0
      ? i18n.t(`errors.${2004401 + index}`)
      : zh
        ? "暂时无法完成，请重试或调整模型。"
        : "Unable to complete this run. Retry or choose another model.";
  };
  const labels: Record<string, string> = zh
    ? {
        queued: "正在排队",
        waiting_source: "正在准备论文结构",
        analyzing: "正在梳理论文主线与依据",
        writing: "正在撰写并核验后续章节",
        reviewing: "正在核对全文依据",
        completed: "导读已就绪",
        paused: "生成已中断，已完成内容保留",
        canceled: "已取消后续生成，已完成内容保留",
        failed: "生成失败，已完成内容保留",
        source_changed: "原文已变化，请基于新原文生成",
      }
    : {
        queued: "Queued",
        waiting_source: "Preparing the paper structure",
        analyzing: "Tracing the argument and evidence",
        writing: "Writing and reviewing upcoming sections",
        reviewing: "Reviewing the complete guide",
        completed: "Guide ready",
        paused: "Generation interrupted; published passages remain available",
        canceled: "Generation canceled; published passages remain available",
        failed: "Generation failed; published passages remain available",
        source_changed: "Source changed; generate from the new paper",
      };
  function renderAsset(block: PaperGuideBlock) {
    return (
      <GuideInlineAsset
        key={`${guide.revision?.id}/${block.id}`}
        id={block.asset_id}
        revision={guide.revision!.id}
        target={target}
        preview={preview}
        language={language}
        current={guide.revision!.source_current}
      />
    );
  }
  return (
    <div
      ref={container}
      className={`paper-reading-workspace${showGuide ? " is-guide" : ""}${sourceOpen ? " has-source" : ""}${auxVisible ? " has-auxiliary" : ""}`}
      data-dual={dual}
    >
      {p.eligible && canEnter && (
        <header className="paper-guide-toolbar">
          {showGuide && p.back}
          <div role="group" aria-label={zh ? "阅读模式" : "Reading mode"}>
            <button
              aria-pressed={!showGuide}
              onClick={() => setReadingMode("source")}
            >
              {zh ? "原文" : "Original"}
            </button>
            <button
              aria-pressed={showGuide}
              onClick={() => setReadingMode("article")}
            >
              {zh ? "导读" : "Guide"}
            </button>
          </div>
          {showGuide && (
            <>
              <button
                aria-pressed={sourceOpen}
                onClick={() => {
                  setSourceOpen(!sourceOpen);
                  p.onAuxiliary(undefined);
                }}
              >
                {sourceOpen
                  ? zh
                    ? "返回导读"
                    : "Back to guide"
                  : zh
                    ? "对照原文"
                    : "Compare original"}
              </button>
              {sourceOpen && p.onReturnSource && (
                <button onClick={p.onReturnSource}>
                  {zh ? "返回上个原文位置" : "Previous source position"}
                </button>
              )}
              {[
                ["chat", zh ? "追问" : "Assistant"],
                ["notes", zh ? "笔记" : "Notes"],
                ["translation", zh ? "翻译" : "Translation"],
              ].map(([id, label]) => (
                <button
                  key={id}
                  aria-pressed={auxVisible && p.auxiliaryTab === id}
                  onClick={() => {
                    setSourceOpen(false);
                    p.onAuxiliary(
                      auxVisible && p.auxiliaryTab === id ? undefined : id,
                    );
                  }}
                >
                  {label}
                </button>
              ))}
              <PaperGuideGenerationDetails
                zh={zh}
                model={model}
                setModel={setModel}
                models={models}
                revision={guide.revision}
                busy={busy}
                canGenerate={!!guide.status?.can_generate}
                active={active}
                onRegenerate={() => void run("regenerate")}
              />
            </>
          )}
        </header>
      )}
      <div className="paper-reading-panes">
        {showGuide && (
          <main
            className="paper-guide-main"
            hidden={!dual && (sourceOpen || auxVisible)}
          >
            {Boolean(error || guide.error) && (
              <p role="alert">
                {error || getLocalizedErrorMessage(guide.error)}
              </p>
            )}
            {guide.availableRevision && guide.revision && (
              <div className="paper-guide-status">
                <span>
                  {zh
                    ? "新版已就绪，当前保留你正在读的版本。"
                    : "A new version is ready. Your current version remains open."}
                </span>
                <button
                  onClick={() =>
                    void guide.selectRevision(guide.availableRevision!.id)
                  }
                >
                  {zh ? "阅读新版" : "Read new version"}
                </button>
              </div>
            )}
            {stage && stage !== "completed" && (
              <div className="paper-guide-status" role="status">
                <span>
                  {labels[stage] || (zh ? "正在准备" : "Preparing")}{" "}
                  {(requiresRegeneration || candidate?.run.error_code) && (
                    <span>
                      {reasonText(
                        requiresRegeneration
                          ? "paper_guide_regeneration_required"
                          : candidate?.run.error_code,
                      )}
                    </span>
                  )}
                </span>
                {active ? (
                  <button disabled={busy} onClick={() => void run("cancel")}>
                    {zh ? "取消后续生成" : "Cancel remaining generation"}
                  </button>
                ) : (
                  (requiresRegeneration || resumable) && (
                    <>
                      {!requiresRegeneration && (
                        <button
                          disabled={busy || !guide.status?.can_generate}
                          onClick={() => void run("resume")}
                        >
                          {zh ? "继续生成" : "Resume"}
                        </button>
                      )}
                      <button
                        disabled={busy || !guide.status?.can_generate}
                        onClick={() => void run("regenerate")}
                      >
                        {zh ? "重新生成导读" : "Generate a new guide"}
                      </button>
                    </>
                  )
                )}
              </div>
            )}
            {guide.revision && !guide.revision.source_current && (
              <p className="paper-guide-warning">
                {zh
                  ? "历史版本：原文已变化，来源跳转已停用。"
                  : "Historical version: source changed; source navigation is disabled."}
              </p>
            )}
            {guide.revision?.status === "limited" && (
              <p className="paper-guide-warning">
                {zh
                  ? "有限完成：部分材料未能解读，请查看文末覆盖范围。"
                  : "Limited coverage: some materials could not be interpreted. See coverage below."}
              </p>
            )}
            {guide.revision?.sections.length ? (
              <PaperGuideReader
                revision={guide.revision}
                title={metadata?.title || p.title}
                metadata={metadata}
                language={language}
                compact={sourceOpen || auxVisible || !dual}
                progress={localProgress.current || guide.status?.progress}
                onProgress={saveProgress}
                onEvidence={requestEvidence}
                onSelection={(selection) => {
                  setSourceOpen(false);
                  p.onSelection(selection);
                  p.onAuxiliary("chat");
                }}
                renderAsset={renderAsset}
              />
            ) : (
              <div className="paper-guide-empty">
                <h1>{p.title}</h1>
                <h2>
                  {zh
                    ? "沿着论文的论证读懂全文"
                    : "Read through the paper’s argument"}
                </h2>
                <p>
                  {zh
                    ? "从研究背景与核心问题开始，理解主要贡献，并逐项对照原文依据。"
                    : "Understand the background, central question, contribution, and supporting evidence."}
                </p>
                {!active && !candidate && (
                  <>
                    <button
                      disabled={busy || !guide.status?.can_generate}
                      onClick={() => void run("generate")}
                    >
                      {p.ir
                        ? zh
                          ? "生成论文导读"
                          : "Generate paper guide"
                        : zh
                          ? "准备论文并生成导读"
                          : "Prepare paper and generate guide"}
                    </button>
                    {!p.ir && (
                      <p>
                        {zh
                          ? "先解析论文结构，再生成导读。"
                          : "The paper structure will be parsed before generation."}
                      </p>
                    )}
                    {guide.status?.unavailable_reason && (
                      <p>{reasonText(guide.status.unavailable_reason)}</p>
                    )}
                  </>
                )}
              </div>
            )}
          </main>
        )}
        <div className="paper-reading-source" hidden={!sourceVisible}>
          {p.source}
        </div>
        {!showGuide && p.divider}
        <div className="paper-reading-auxiliary" hidden={!auxVisible}>
          {p.auxiliary}
        </div>
      </div>
      {!showGuide && p.dock}
      {(evidence || evidenceLoading) && (
        <Modal
          open
          title={zh ? "来源预览" : "Source preview"}
          footer={null}
          onCancel={() => {
            evidenceAbort.current?.abort();
            setEvidence(undefined);
            setEvidenceLoading(false);
          }}
          width={720}
        >
          {evidence ? (
            <PaperGuideEvidencePreview
              value={evidence}
              currentSourceHash={p.ir?.source_sha256}
              revisionSourceHash={guide.revision!.source_sha256}
              language={language}
              onClose={() => {
                setEvidence(undefined);
                setEvidenceLoading(false);
              }}
              onNavigate={(e) => {
                setSourceOpen(true);
                p.onAuxiliary(undefined);
                setEvidence(undefined);
                p.onNavigate(e);
              }}
            >
              {["figure", "chart", "table", "formula", "image"].includes(
                evidence.evidence.source_type,
              ) &&
                evidence.source_current && (
                  <PaperGuideAsset
                    evidence={evidence.evidence}
                    preview={preview}
                    language={language}
                  />
                )}
            </PaperGuideEvidencePreview>
          ) : (
            <p role="status">{zh ? "正在读取来源…" : "Loading source…"}</p>
          )}
        </Modal>
      )}
    </div>
  );
}
function GuideInlineAsset({
  id,
  revision,
  target,
  preview,
  language,
  current,
}: {
  id?: string;
  revision: string;
  target: Parameters<typeof paperGuideApi.evidence>[0];
  preview: GuidePreview;
  language: string;
  current: boolean;
}) {
  const [e, setEvidence] = useState<PaperGuideEvidence>();
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    setEvidence(undefined);
    setFailed(false);
    if (!id || !current) return;
    const abort = new AbortController();
    void paperGuideApi
      .evidence(target, revision, id, abort.signal)
      .then((v) => {
        if (!abort.signal.aborted) {
          if (v.source_current) setEvidence(v.evidence);
          else setFailed(true);
        }
      })
      .catch(() => {
        if (!abort.signal.aborted) setFailed(true);
      });
    return () => abort.abort();
  }, [id, revision, target, current]);
  return (
    <PaperGuideAsset
      evidence={e}
      unavailable={failed || !current || !id}
      preview={preview}
      language={language}
    />
  );
}
