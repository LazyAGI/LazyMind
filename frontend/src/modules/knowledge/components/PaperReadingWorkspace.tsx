import GuideSectionIcon from "./PaperGuide/GuideSectionIcon";
import GuideGenerationProgress, { GuideSkeleton } from "./PaperGuide/GuideGenerationProgress";
import { Modal, Popover } from "antd";
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
import { usePaperReaderSession } from "../api/paperReader";
import { paperGuideApi } from "../api/paperGuide";
import { usePaperGuide } from "./PaperGuide/usePaperGuide";
import { isGuideRunActive } from "./PaperGuide/guideRunState";
import PaperGuideReader from "./PaperGuide/PaperGuideReader";
import PaperGuideEvidencePreview from "./PaperGuide/PaperGuideEvidence";
import PaperGuideAsset, {
  type GuidePreview,
} from "./PaperGuide/PaperGuideAsset";
import { useGuideSourcePreview } from "./PaperGuide/useGuideSourcePreview";
import type { PdfIRDocument } from "../api/pdfIR";

const qualityStopCode = "paper_guide_review_required";
const regenerationCode = "paper_guide_regeneration_required";

interface Props {
  dataset: string;
  document: string;
  title: string;
  eligible: boolean;
  source: ReactNode | ((readingModeSwitch: ReactNode) => ReactNode);
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
  getPdfData: () => ArrayBuffer | null | Promise<ArrayBuffer>;
  getPdfVersion?: () => object | null;
  ir?: PdfIRDocument;
  onModeChange?: (guide: boolean) => void;
}
export default function PaperReadingWorkspace(p: Props) {
  const { i18n } = useTranslation();
  const zh = i18n.language.startsWith("zh");
  const account = usePaperReaderSession();
  const languageKey = `paper-guide-language/${account}/${p.dataset}/${p.document}`;
  const [languageChoice, setLanguageChoice] = useState<{ key: string; value: "zh" | "en" }>();
  const savedLanguage = localStorage.getItem(languageKey);
  const language = languageChoice?.key === languageKey ? languageChoice.value
    : savedLanguage === "zh" || savedLanguage === "en" ? savedLanguage : zh ? "zh" : "en";
  const [draftLanguage, setDraftLanguage] = useState<{ key: string; value: "zh" | "en" }>();
  const generationLanguage = draftLanguage?.key === languageKey ? draftLanguage.value : language;
  const selectLanguage = (value: "zh" | "en") => {
    setLanguageChoice({ key: languageKey, value });
    setDraftLanguage(undefined);
    localStorage.setItem(languageKey, value);
    setModeChoice({ key: storageKey, value: "article" });
    localStorage.setItem(storageKey, "article");
  };
  const guide = usePaperGuide(p.dataset, p.document, language, p.eligible);
  const storageKey = `paper-guide-mode/${guide.identity}/${p.dataset}/${p.document}`;
  const [modeChoice, setModeChoice] = useState<{ key: string; value: string }>();
  const savedMode = modeChoice?.key === storageKey ? modeChoice.value : localStorage.getItem(storageKey);
  const [mountedSourceKey, setMountedSourceKey] = useState<string>();
  const [guideChatKey, setGuideChatKey] = useState<string>();
  const [sourceOpen, setSourceOpen] = useState(false);
  const [width, setWidth] = useState(0);
  const container = useRef<HTMLDivElement>(null);
  const [model, setModel] = useState("");
  const [regenerateOpen, setRegenerateOpen] = useState(false);
  const [models, setModels] = useState<ReadingProviderSelection>();
  const [busy, setBusy] = useState(false);
  const [pendingGeneration, setPendingGeneration] = useState(false);
  const [error, setError] = useState("");
  const candidate = guide.status?.candidate_revision || guide.revision;
  const mutationScope = `${storageKey}/${language}/${candidate?.id || ""}`;
  const latestMutationScope = useRef(mutationScope);
  latestMutationScope.current = mutationScope;
  const mutationSequence = useRef(0);
  const [rejectedResume, setRejectedResume] = useState<{
    scope: string;
    reason: typeof qualityStopCode | typeof regenerationCode;
  }>();
  const resumeRejection =
    rejectedResume?.scope === mutationScope ? rejectedResume.reason : undefined;
  const requiresRegeneration =
    candidate?.run.error_code === regenerationCode ||
    resumeRejection === regenerationCode;
  const qualityBlocked =
    candidate?.run.resume_block_reason === qualityStopCode ||
    candidate?.run.error_code === qualityStopCode ||
    resumeRejection === qualityStopCode;
  useEffect(() => {
    mutationSequence.current += 1;
    setRejectedResume(undefined);
    setError("");
    setBusy(false);
  }, [mutationScope]);
  const [evidence, setEvidence] = useState<PaperGuideEvidenceResponse>();
  const [evidenceLoading, setEvidenceLoading] = useState(false);
  const evidenceAbort = useRef<AbortController>();
  const evidenceOpener = useRef<HTMLButtonElement>();
  const latest = useRef("");
  latest.current = `${storageKey}/${language}`;
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
  const restoringGuide = !guide.status && !guide.error && savedMode === "article";
  const showGuide = p.eligible && (restoringGuide || (canEnter &&
    (savedMode === "article" || (!savedMode && !!guide.status?.published_revision))));
  useEffect(() => {
    if (showGuide && !savedMode) localStorage.setItem(storageKey, "article");
  }, [showGuide, savedMode, storageKey]);
  const revisionId = guide.revision?.id;
  const evidenceCurrent = !!(
    evidence?.source_current && guide.revision?.source_current
  );
  useEffect(() => {
    setGuideChatKey(undefined);
    setSourceOpen(false);
    setError("");
    setBusy(false);
    setModel("");
    setRegenerateOpen(false);
    setModels(undefined);
    localProgress.current = undefined;
    opened.current = false;
    return () => {
      evidenceAbort.current?.abort();
      clearTimeout(progressTimer.current);
    };
  }, [storageKey, language]);
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
        if (latest.current === `${storageKey}/${language}`)
          setModels(v.provider_selections?.study);
      })
      .catch(() => {});
  }, [showGuide, storageKey, language]);
  const setReadingMode = (value: string) => {
    setModeChoice({ key: storageKey, value });
    setGuideChatKey(undefined);
    localStorage.setItem(storageKey, value);
    setSourceOpen(false);
    if (value === "article") p.onAuxiliary(undefined);
  };
  const run = async (
    action: "generate" | "regenerate" | "cancel" | "resume",
  ) => {
    const sequence = ++mutationSequence.current;
    const isCurrent = () =>
      latestMutationScope.current === mutationScope &&
      mutationSequence.current === sequence;
    setBusy(true);
    if (action === "generate" || action === "regenerate" || action === "resume") setPendingGeneration(true);
    setError("");
    try {
      if (action === "generate" || action === "regenerate") {
        await paperGuideApi.generate({ ...target, language: generationLanguage }, {
          language: generationLanguage,
          expected_source_hash: guide.status?.source_sha256 || "",
          idempotency_key: crypto.randomUUID(),
          regenerate: action === "regenerate",
          ...(model ? { model_ref: model } : {}),
        });
      } else if (candidate) {
        await paperGuideApi[action](target, candidate.id);
      }
      if (isCurrent()) {
        if ((action === "generate" || action === "regenerate") && generationLanguage !== language) {
          setEvidence(undefined);
          evidenceAbort.current?.abort();
          selectLanguage(generationLanguage);
        } else await guide.refresh();
      }
    } catch (e) {
      setPendingGeneration(false);
      if (isCurrent()) {
        const code = action === "resume" ? extractErrorCode(e) : undefined;
        const reason =
          code === "2004413"
            ? regenerationCode
            : code === "2004411" || code === qualityStopCode
              ? qualityStopCode
              : undefined;
        if (reason) {
          // Keep this revision blocked if the next status fetch fails or is stale.
          setRejectedResume({ scope: mutationScope, reason });
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
      if (!revisionId) return;
      const key = `${storageKey}/${language}`;
      evidenceAbort.current?.abort();
      const abort = new AbortController();
      evidenceAbort.current = abort;
      setEvidence(undefined);
      setEvidenceLoading(true);
      try {
        const e = await paperGuideApi.evidence(
          target,
          revisionId,
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
    [storageKey, language, target, revisionId],
  );
  const openEvidence = (id: string, opener: HTMLButtonElement) => {
    evidenceOpener.current = opener;
    void requestEvidence(id);
  };
  const closeEvidence = useCallback(() => {
    evidenceAbort.current?.abort();
    setEvidence(undefined);
    setEvidenceLoading(false);
  }, []);
  useEffect(() => {
    evidenceOpener.current = undefined;
    closeEvidence();
  }, [storageKey, target, revisionId, showGuide, closeEvidence]);
  const currentSourceHash = p.ir?.source_sha256 ?? guide.status?.source_sha256;
  const { preview, sourceVerified, verifyCurrentSource } = useGuideSourcePreview(
    `${storageKey}/${revisionId ?? ""}`,
    guide.revision?.source_sha256,
    !!guide.revision?.source_current,
    currentSourceHash,
    p.getPdfData,
    p.getPdfVersion,
  );
  const saveProgress = (progress: PaperGuideProgressRequest) => {
    localProgress.current = progress;
    clearTimeout(progressTimer.current);
    progressTimer.current = setTimeout(() => {
      void paperGuideApi.progress(target, progress).catch(() => {});
    }, 700);
  };
  const sourceVisible = !showGuide || sourceOpen;
  useEffect(() => {
    if (sourceVisible) setMountedSourceKey(storageKey);
  }, [sourceVisible, storageKey]);
  const mountSource = sourceVisible || mountedSourceKey === storageKey;
  const mountAuxiliary = mountSource || guideChatKey === storageKey;
  const auxVisible = mountAuxiliary && p.auxiliaryOpen && !sourceOpen;
  const dual = width >= 1100;
  const stage = candidate?.run.stage;
  const resumable = !!stage && ["paused", "failed", "canceled"].includes(stage);
  const active = isGuideRunActive(stage);
  useEffect(() => { if (active || resumable || stage === "completed") setPendingGeneration(false); }, [active, resumable, stage]);
  useEffect(() => { setPendingGeneration(false); }, [storageKey, language]);
  const generating = active || pendingGeneration;
  const generationClock = useRef<{ active: boolean; startedAt: number; generationStartedAt?: number }>({ active: false, startedAt: Date.now() });
  if (generating && !generationClock.current.active) {
    generationClock.current.startedAt = Date.now();
    generationClock.current.generationStartedAt = undefined;
  }
  if (generating && active && stage && stage !== "queued" && generationClock.current.generationStartedAt === undefined) {
    generationClock.current.generationStartedAt = Date.now();
  }
  generationClock.current.active = generating;
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
    "model_timeout",
    "model_request_failed",
  ];
  const reasonText = (value?: string) => {
    if (!value) return "";
    if(value === "paper_guide_source_precision_unavailable") return zh
      ? "无法从保留的解析文件核实来源物理页，暂不能生成新导读。原文及历史导读仍可阅读。"
      : "Source pages could not be verified from the retained parse archive. New guides are unavailable; the original paper and historical guides remain readable.";
    const index = errorNames.indexOf(value.replace(/^paper_guide_/, ""));
    return index >= 0
      ? i18n.t(`errors.${2004401 + index}`)
      : zh
        ? "暂时无法完成，请重试或调整模型。"
        : "Unable to complete this run. Retry or choose another model.";
  };
  const qualityHelp = zh
    ? "当前版本无法继续生成，请重新生成导读。"
    : "This revision can’t continue. Generate a new guide.";
  const runReason = requiresRegeneration
    ? reasonText(regenerationCode)
    : qualityBlocked
      ? qualityHelp
      : reasonText(candidate?.run.error_code);
  const labels: Record<string, string> = zh
    ? {
        queued: "正在排队",
        waiting_source: "正在准备论文结构",
        reading: "正在读取论文",
        planning: "正在规划章节",
        checking: "正在检查章节格式",
        analyzing: "正在梳理论文主线与依据",
        writing: "正在并行撰写章节",
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
        reading: "Reading the paper",
        planning: "Planning chapters",
        checking: "Checking chapter format",
        analyzing: "Tracing the argument and evidence",
        writing: "Writing chapters in parallel",
        reviewing: "Reviewing the complete guide",
        completed: "Guide ready",
        paused: "Generation interrupted; published passages remain available",
        canceled: "Generation canceled; published passages remain available",
        failed: "Generation failed; published passages remain available",
        source_changed: "Source changed; generate from the new paper",
      };
  function openAssetPage(evidence: PaperGuideEvidence) {
    void verifyCurrentSource().then(current => {
      if (!current?.()) return;
      setSourceOpen(true);
      p.onAuxiliary(undefined);
      p.onNavigate(evidence);
    }).catch(() => {});
  }
  function renderAsset(block: PaperGuideBlock) {
    return (
      <GuideInlineAsset
        key={`${guide.revision?.id}/${block.id}`}
        compactCaption={guide.revision?.plan?.layout === "flashpapers"}
        captionText={block.kind === "figure" ? block.text : undefined}
        id={block.asset_id}
        revision={guide.revision!.id}
        target={target}
        preview={preview}
        language={language}
        current={guide.revision!.source_current}
        onEvidence={openEvidence}
        onOpenPage={sourceVerified ? openAssetPage : undefined}
      />
    );
  }
  const readingModeSwitch = p.eligible && (canEnter || restoringGuide) ? (
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
  ) : null;
  const generationProgress = generating && <GuideGenerationProgress generationStartedAt={generationClock.current.generationStartedAt} startedAt={generationClock.current.startedAt} compact zh={zh} stage={pendingGeneration && !active ? "submitting" : stage || "queued"} total={candidate?.id === guide.revision?.id ? guide.revision?.plan?.sections.length || 0 : 0} done={candidate?.id === guide.revision?.id ? guide.revision?.sections.filter(section => section.blocks.length > 0).length || 0 : 0}/>;
  const toolbarDetails = <div className="guide-toolbar-details">
    {generating && <GuideGenerationProgress generationStartedAt={generationClock.current.generationStartedAt} startedAt={generationClock.current.startedAt} zh={zh} stage={pendingGeneration && !active ? "submitting" : stage || "queued"} total={candidate?.id === guide.revision?.id ? guide.revision?.plan?.sections.length || 0 : 0} done={candidate?.id === guide.revision?.id ? guide.revision?.sections.filter(section => section.blocks.length > 0).length || 0 : 0}/>}
            {Boolean(error || guide.error) && (
              <p role="alert" className="guide-error-notice">
                <GuideSectionIcon role="limitations"/>{error || getLocalizedErrorMessage(guide.error)}
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
                  {guide.revision?.id === candidate?.id && guide.revision?.plan?.layout === "flashpapers" && guide.revision.plan.sections.length > 0 && <span>{guide.revision.sections.filter(section => section.blocks.length > 0).length} / {guide.revision.plan.sections.length} {zh ? "章已完成" : "chapters ready"} </span>}
                  {runReason && <span>{runReason}</span>}
                </span>
                {active ? (
                  <button disabled={busy} onClick={() => void run("cancel")}>
                    {zh ? "取消后续生成" : "Cancel remaining generation"}
                  </button>
                ) : (
                  (requiresRegeneration || qualityBlocked || resumable) && (
                    <>
                      <label>
                        {zh ? "本次研读模型" : "Model for this run"}
                        <select value={model} onChange={(event) => setModel(event.target.value)}>
                          <option value="">{zh ? "跟随阅读偏好" : "Use reading preference"}</option>
                          {models?.options.map(option => <option key={option.id} value={option.id}>{option.label}</option>)}
                        </select>
                      </label>
                      {!requiresRegeneration && !qualityBlocked && (
                        <button
                          disabled={busy || !guide.status?.can_generate}
                          onClick={() => void run("resume")}
                        >
                          {zh ? "继续生成" : "Resume"}
                        </button>
                      )}
                      <button
                        disabled={busy || !guide.status?.can_generate}
                        onClick={() => { setDraftLanguage({ key: languageKey, value: language }); setRegenerateOpen(true); }}
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
                  ? "历史版本：原文或来源定位已更新，来源跳转和导读追问已停用。"
                  : "Historical version: source content or positioning was updated; source navigation and guide follow-up are disabled."}
              </p>
            )}
            {guide.revision?.status === "limited" && guide.revision.plan?.layout !== "flashpapers" && (
              <p className="paper-guide-warning">
                {zh
                  ? "有限完成：部分材料未能解读，请查看文末覆盖范围。"
                  : "Limited coverage: some materials could not be interpreted. See coverage below."}
              </p>
            )}

  </div>;
  const languageSelect = <label className="guide-language-field">{zh ? "导读语言" : "Guide language"}
    <select value={generationLanguage} disabled={busy || generating} onChange={event => setDraftLanguage({ key: languageKey, value: event.target.value as "zh" | "en" })}>
      <option value="zh">简体中文</option><option value="en">English</option>
    </select>
  </label>;
  const hasDetails = Boolean(generating || error || guide.error || guide.availableRevision || (stage && stage !== "completed") || (guide.revision && !guide.revision.source_current) || guide.revision?.status === "limited");
  return (
    <div
      ref={container}
      className={`paper-reading-workspace${showGuide ? " is-guide" : ""}${sourceOpen ? " has-source" : ""}${auxVisible ? " has-auxiliary" : ""}`}
      data-dual={dual}
    >
      {readingModeSwitch && (showGuide || typeof p.source !== "function") && (
        <header className="paper-guide-toolbar">
          <div className="paper-guide-back">{showGuide && p.back}</div>{readingModeSwitch}
          {showGuide && <div className="guide-toolbar-actions">
            {(guide.revision || savedLanguage === "zh" || savedLanguage === "en") && <select className="guide-reading-language" aria-label={zh ? "阅读语言" : "Reading language"} value={language} disabled={busy || generating} onChange={event => selectLanguage(event.target.value as "zh" | "en")}><option value="zh">简体中文</option><option value="en">English</option></select>}
            {hasDetails && <Popover trigger="click" placement="bottomRight" getPopupContainer={node => node.parentElement!} content={toolbarDetails}>
              <button className="guide-status-trigger" aria-label={zh ? "生成状态与操作" : "Generation status and actions"}>
                {generating ? generationProgress : <span>{error || guide.error ? (zh ? "生成遇到问题" : "Generation issue") : guide.availableRevision ? (zh ? "新版已就绪" : "New version ready") : labels[stage || ""] || (zh ? "版本提示" : "Version notice")}</span>}
                <span aria-hidden="true">⌄</span>
              </button>
            </Popover>}
            {guide.revision?.run.stage === "completed" && !generating && <button className="guide-regenerate-trigger" disabled={busy} onClick={() => { setDraftLanguage({ key: languageKey, value: language }); setRegenerateOpen(true); }}>{zh ? "重新生成" : "Regenerate"}</button>}
          </div>}
        </header>
      )}
      <Modal open={regenerateOpen && showGuide} title={zh ? "重新生成论文导读" : "Regenerate paper guide"} onCancel={() => setRegenerateOpen(false)}
        okText={zh ? "开始重新生成" : "Start regeneration"} cancelText={zh ? "取消" : "Cancel"}
        okButtonProps={{ disabled: busy || generating || !guide.status?.can_generate }}
        onOk={() => { setRegenerateOpen(false); void run("regenerate"); }}>
        <div className="guide-regenerate-dialog">
          <p>{zh ? "选择本次导读的语言和模型。同语言生成期间可继续阅读当前版本；切换语言后会显示对应语言的导读。" : "Choose the guide language and model. Keep reading during same-language regeneration; choosing another language opens its guide."}</p>
          {languageSelect}
          <label>{zh ? "本次生成模型" : "Model for this run"}
            <select value={model} onChange={event => setModel(event.target.value)}>
              <option value="">{zh ? "跟随阅读偏好" : "Use reading preference"}</option>
              {models?.options.map(option => <option key={option.id} value={option.id}>{option.label}</option>)}
            </select>
          </label>
          <p>{zh ? "预计约 2 分钟，实际时间随论文长度和模型速度变化。" : "Usually about 2 minutes, depending on the paper and model."}</p>
          {!guide.status?.can_generate && <p role="alert">{reasonText(guide.status?.unavailable_reason)}</p>}
        </div>
      </Modal>
      <div className="paper-reading-panes">
        {showGuide && (
          <main
            className="paper-guide-main"
            hidden={!dual && (sourceOpen || auxVisible)}
          >
            {guide.revision && (guide.revision.sections.length > 0 || guide.revision.plan?.layout === "flashpapers") ? (
              <PaperGuideReader
                revision={guide.revision}
                title={p.title}
                language={language}
                compact={sourceOpen || auxVisible || width < 720}
                progress={localProgress.current || guide.status?.progress}
                onProgress={saveProgress}
                onEvidence={openEvidence}
                onSelection={(selection) => {
                  setSourceOpen(false);
                  setGuideChatKey(storageKey);
                  p.onSelection(selection);
                  p.onAuxiliary("chat");
                }}
                renderAsset={renderAsset}
              />
            ) : generating || restoringGuide ? (<div className="paper-guide-preparing" aria-busy="true"><GuideSkeleton/></div>) : (
              <div className="paper-guide-empty">
                <div className="guide-empty-symbol"><GuideSectionIcon role="glossary"/></div>
                <div className="guide-article-kicker">{zh ? "论文导读" : "PAPER GUIDE"}</div>
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
                <div className="guide-empty-features">
                  {[["big-idea", zh ? "核心思路" : "Key ideas"], ["how-it-works", zh ? "方法拆解" : "How it works"], ["playground", zh ? "交互探索" : "Interactive exploration"]].map(([role, label]) => <span key={role}><GuideSectionIcon role={role}/>{label}</span>)}
                </div>
                {!active && !candidate && (
                  <>
                    {languageSelect}
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
          {mountSource && (typeof p.source === "function" ? p.source(showGuide ? null : readingModeSwitch) : p.source)}
        </div>
        {!showGuide && p.divider}
        <div className="paper-reading-auxiliary" hidden={!auxVisible}>
          {mountAuxiliary && p.auxiliary}
        </div>
      </div>
      {!showGuide && p.dock}
      <Modal
        open={showGuide && !!revisionId && (!!evidence || evidenceLoading)}
        title={zh ? "来源预览" : "Source preview"}
        footer={null}
        destroyOnHidden
        focusTriggerAfterClose={false}
        onCancel={closeEvidence}
        afterClose={() => {
          const opener = evidenceOpener.current;
          evidenceOpener.current = undefined;
          if (
            opener?.isConnected &&
            opener.getClientRects().length &&
            getComputedStyle(opener).visibility === "visible"
          ) {
            opener.focus({ preventScroll: true });
          }
        }}
        width={720}
      >
        {evidence && guide.revision ? (
          <PaperGuideEvidencePreview
            value={{ ...evidence, source_current: evidenceCurrent }}
            currentSourceHash={currentSourceHash}
            revisionSourceHash={guide.revision.source_sha256}
            language={language}
            onClose={closeEvidence}
            onNavigate={(e) => {
              evidenceOpener.current = undefined;
              closeEvidence();
              setSourceOpen(true);
              p.onAuxiliary(undefined);
              p.onNavigate(e);
            }}
          >
            {["figure", "chart", "table", "formula", "image"].includes(
              evidence.evidence.source_type,
            ) && evidenceCurrent && (
              <PaperGuideAsset
                onEvidence={requestEvidence}
                evidence={evidence.evidence}
                preview={preview}
                language={language}
              />
            )}
          </PaperGuideEvidencePreview>
        ) : evidenceLoading ? (
          <p role="status">{zh ? "正在读取来源…" : "Loading source…"}</p>
        ) : null}
      </Modal>
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
  onEvidence,
  onOpenPage,
  compactCaption,
  captionText,
}: {
  captionText?: string;
  compactCaption?: boolean;
  id?: string;
  revision: string;
  target: Parameters<typeof paperGuideApi.evidence>[0];
  preview: GuidePreview;
  language: string;
  current: boolean;
  onEvidence: (id: string, opener: HTMLButtonElement) => void;
  onOpenPage?: (evidence: PaperGuideEvidence) => void;
}) {
  const [loaded, setEvidence] = useState<{
    id: string;
    revision: string;
    target: typeof target;
    value: PaperGuideEvidenceResponse;
  }>();
  const value = current && loaded?.id === id && loaded?.revision === revision && loaded?.target === target
    ? loaded.value : undefined;
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
          if (v.source_current && v.evidence.id === id) setEvidence({ id, revision, target, value: v });
          else setFailed(true);
        }
      })
      .catch(() => {
        if (!abort.signal.aborted) setFailed(true);
      });
    return () => abort.abort();
  }, [id, revision, target, current]);
  return (
    <PaperGuideAsset compactCaption={compactCaption} captionText={captionText}
      evidence={value?.evidence}
      originalDisplay={value?.original_display}
      onEvidence={onEvidence}
      onOpenPage={onOpenPage}
      unavailable={failed || !current || !id}
      preview={preview}
      language={language}
    />
  );
}
