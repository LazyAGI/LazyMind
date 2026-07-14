import { CheckOutlined, CopyOutlined, FullscreenOutlined } from "@ant-design/icons";
import { Modal, Tooltip, message } from "antd";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { highlightCode } from "./syntaxHighlight";

type MermaidView = "diagram" | "source";
type CopyStatus = "idle" | "copying" | "copied" | "failed";
type RenderState =
  | { status: "idle" | "rendering"; svg: string; error: string }
  | { status: "success"; svg: string; error: string }
  | { status: "error"; svg: string; error: string };

let mermaidInitialized = false;
let mermaidBlockSequence = 0;
const mermaidRenderCache = new Map<string, string>();
const MERMAID_RENDER_CACHE_LIMIT = 80;

async function getMermaid() {
  const mermaidModule = await import("mermaid");
  return mermaidModule.default;
}

function ensureMermaidInitialized(
  mermaid: Awaited<ReturnType<typeof getMermaid>>,
) {
  if (mermaidInitialized) {
    return;
  }

  mermaid.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    theme: "default",
  });
  mermaidInitialized = true;
}

function getMermaidRenderId() {
  mermaidBlockSequence += 1;
  return `rag-mermaid-${Date.now()}-${mermaidBlockSequence}`;
}

function cacheMermaidRender(code: string, svg: string) {
  mermaidRenderCache.set(code, svg);

  if (mermaidRenderCache.size > MERMAID_RENDER_CACHE_LIMIT) {
    const oldestKey = mermaidRenderCache.keys().next().value;
    if (oldestKey) {
      mermaidRenderCache.delete(oldestKey);
    }
  }
}

async function copyTextToClipboard(text: string) {
  if (!text.trim()) {
    throw new Error("Empty source");
  }

  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return;
    }
  } catch {
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.top = "0";
  textarea.style.left = "0";
  textarea.style.width = "1px";
  textarea.style.height = "1px";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";

  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, text.length);

  try {
    const copied = document.execCommand("copy");
    if (!copied) {
      throw new Error("Copy command failed");
    }
  } finally {
    document.body.removeChild(textarea);
  }
}

function getCopyTooltip(status: CopyStatus) {
  const key = status === "copied"
    ? "chat.markdownCopied"
    : status === "failed"
      ? "chat.markdownCopyFailed"
      : "chat.markdownCopySource";
  return key;
}

function getCopyAnnouncement(status: CopyStatus) {
  return status === "copied"
    ? "chat.markdownSourceCopied"
    : status === "failed"
      ? "chat.markdownSourceCopyFailed"
      : "";
}

const MermaidSource = ({ code }: { code: string }) => {
  const highlighted = useMemo(() => highlightCode(code, "mermaid"), [code]);

  return (
    <pre className="md-code-source">
      {highlighted ? (
        <code
          className="language-mermaid"
          dangerouslySetInnerHTML={{ __html: highlighted }}
        />
      ) : (
        <code className="language-mermaid">{code}</code>
      )}
    </pre>
  );
};

const MermaidDiagram = ({
  svg,
  onOpen,
}: {
  svg: string;
  onOpen: () => void;
}) => {
  const { t } = useTranslation();
  return (
    <button
      aria-label={t("chat.markdownEnlargeDiagram")}
      className="md-mermaid-preview"
      type="button"
      onClick={onOpen}
    >
      <span dangerouslySetInnerHTML={{ __html: svg }} />
    </button>
  );
};

const MermaidBlockComponent = ({
  code,
  isStreaming = false,
}: {
  code: string;
  isStreaming?: boolean;
}) => {
  const { t } = useTranslation();
  const [activeView, setActiveView] = useState<MermaidView>("diagram");
  const [copyStatus, setCopyStatus] = useState<CopyStatus>("idle");
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [renderState, setRenderState] = useState<RenderState>({
    status: "idle",
    svg: "",
    error: "",
  });
  const copyResetTimerRef = useRef<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    const renderId = getMermaidRenderId();
    const cachedSvg = mermaidRenderCache.get(code);

    if (!code.trim()) {
      setRenderState({ status: "error", svg: "", error: "empty" });
      return () => {
        cancelled = true;
      };
    }

    if (cachedSvg) {
      setRenderState({ status: "success", svg: cachedSvg, error: "" });
      return () => {
        cancelled = true;
      };
    }

    setRenderState((previous) => ({
      status: "rendering",
      svg: previous.svg,
      error: "",
    }));

    const renderDiagram = async () => {
      try {
        const mermaid = await getMermaid();
        ensureMermaidInitialized(mermaid);
        await mermaid.parse(code);
        const { svg } = await mermaid.render(renderId, code);

        if (!cancelled) {
          cacheMermaidRender(code, svg);
          setRenderState({ status: "success", svg, error: "" });
        }
      } catch (err) {
        if (!cancelled) {
          setRenderState((previous) => ({
            status: "error",
            svg: isStreaming ? previous.svg : "",
            error: err instanceof Error ? err.message : "render failed",
          }));
        }
      }
    };

    void renderDiagram();

    return () => {
      cancelled = true;
    };
  }, [code, isStreaming]);

  useEffect(() => {
    setCopyStatus("idle");
  }, [activeView, code]);

  useEffect(() => {
    return () => {
      if (copyResetTimerRef.current) {
        window.clearTimeout(copyResetTimerRef.current);
      }
    };
  }, []);

  const visibleView =
    renderState.status === "error" && !isStreaming && activeView === "diagram"
      ? "source"
      : activeView;
  const canShowDiagram = Boolean(renderState.svg);
  const canCopySource = Boolean(code.trim());

  const resetCopyStatusLater = () => {
    if (copyResetTimerRef.current) {
      window.clearTimeout(copyResetTimerRef.current);
    }
    copyResetTimerRef.current = window.setTimeout(() => {
      setCopyStatus("idle");
      copyResetTimerRef.current = null;
    }, 1600);
  };

  const handleCopySource = async () => {
    if (!canCopySource || copyStatus === "copying") {
      return;
    }

    setCopyStatus("copying");
    try {
      await copyTextToClipboard(code);
      setCopyStatus("copied");
      message.success(t("chat.markdownSourceCopied"));
    } catch {
      setCopyStatus("failed");
      message.error(t("chat.copyFailedManual"));
    } finally {
      resetCopyStatusLater();
    }
  };

  return (
    <div className="md-mermaid-block">
      <div className="md-mermaid-toolbar">
        <div className="md-mermaid-tabs" role="tablist" aria-label={t("chat.markdownMermaidDisplay")}>
          <button
            aria-selected={visibleView === "diagram"}
            className={visibleView === "diagram" ? "active" : ""}
            disabled={!canShowDiagram && !isStreaming}
            role="tab"
            type="button"
            onClick={() => setActiveView("diagram")}
          >
            {t("chat.markdownDiagram")}
          </button>
          <button
            aria-selected={visibleView === "source"}
            className={visibleView === "source" ? "active" : ""}
            role="tab"
            type="button"
            onClick={() => setActiveView("source")}
          >
            {t("chat.markdownSource")}
          </button>
        </div>
        <div className="md-mermaid-actions">
          {canShowDiagram && visibleView === "diagram" && (
            <button
              aria-label={t("chat.markdownEnlargeDiagram")}
              className="md-mermaid-icon-button"
              type="button"
              onClick={() => setIsModalOpen(true)}
            >
              <FullscreenOutlined />
            </button>
          )}
          {visibleView === "source" && (
            <Tooltip title={t(getCopyTooltip(copyStatus))}>
              <button
                aria-label={t("chat.markdownCopySource")}
                className={`md-mermaid-icon-button ${
                  copyStatus === "copied" ? "copied" : ""
                }`}
                disabled={!canCopySource || copyStatus === "copying"}
                type="button"
                onClick={handleCopySource}
              >
                {copyStatus === "copied" ? <CheckOutlined /> : <CopyOutlined />}
              </button>
            </Tooltip>
          )}
          <span className="md-mermaid-copy-status" aria-live="polite">
            {getCopyAnnouncement(copyStatus) ? t(getCopyAnnouncement(copyStatus)) : ""}
          </span>
        </div>
      </div>

      {renderState.status === "rendering" && (
        <div className="md-mermaid-status">{t("chat.markdownDiagramRendering")}</div>
      )}
      {renderState.status === "error" && !isStreaming && (
        <div className="md-mermaid-status">{t("chat.markdownDiagramRenderFailed")}</div>
      )}
      {renderState.status === "error" && isStreaming && !canShowDiagram && (
        <div className="md-mermaid-status">{t("chat.markdownDiagramWaitingContent")}</div>
      )}

      {visibleView === "source" ? (
        <MermaidSource code={code} />
      ) : canShowDiagram ? (
        <MermaidDiagram svg={renderState.svg} onOpen={() => setIsModalOpen(true)} />
      ) : (
        <div className="md-mermaid-placeholder" aria-live="polite">
          {t("chat.markdownDiagramGenerating")}
        </div>
      )}

      <Modal
        centered
        className="md-mermaid-modal"
        footer={null}
        open={isModalOpen}
        title={t("chat.markdownDiagram")}
        width="80vw"
        onCancel={() => setIsModalOpen(false)}
      >
        {canShowDiagram && (
          <div
            className="md-mermaid-modal-content"
            dangerouslySetInnerHTML={{ __html: renderState.svg }}
          />
        )}
      </Modal>
    </div>
  );
};

const MermaidBlock = memo(MermaidBlockComponent);

export default MermaidBlock;
