import {
  CheckOutlined,
  CloseOutlined,
  CodeOutlined,
  CopyOutlined,
  DownloadOutlined,
  FileTextOutlined,
  FullscreenOutlined,
  PlayCircleOutlined,
} from "@ant-design/icons";
import { Tooltip, message } from "antd";
import { memo, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";

import { downloadStream } from "@/modules/chat/utils/download";
import { highlightCode } from "./syntaxHighlight";

type HtmlView = "preview" | "source";
type CopyStatus = "idle" | "copying" | "copied" | "failed";

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
      throw new Error();
    }
  } finally {
    document.body.removeChild(textarea);
  }
}

function getCopyTooltip(status: CopyStatus) {
  return status === "copied"
    ? "chat.markdownCopied"
    : status === "failed"
      ? "chat.markdownCopyFailed"
      : "chat.markdownCopySource";
}

function getCopyAnnouncement(status: CopyStatus) {
  return status === "copied"
    ? "chat.markdownSourceCopied"
    : status === "failed"
      ? "chat.markdownSourceCopyFailed"
      : "";
}

const VISIBLE_HTML_PATTERN =
  /<(?:div|section|article|main|header|footer|h[1-6]|p|table|thead|tbody|tr|td|th|ul|ol|li|img|figure|blockquote|pre|button|form|canvas|svg)\b/i;

function stripHtmlTags(value: string) {
  return value.replace(/<[^>]+>/g, "").replace(/\s+/g, " ").trim();
}

function hasRenderableHtml(code: string) {
  const trimmed = code.trim();
  if (!trimmed) {
    return false;
  }

  const bodyMatch = trimmed.match(/<body\b[^>]*>([\s\S]*?)(?:<\/body>|$)/i);
  if (bodyMatch) {
    const bodyContent = bodyMatch[1];
    return (
      VISIBLE_HTML_PATTERN.test(bodyContent) ||
      stripHtmlTags(bodyContent).length > 0
    );
  }

  if (/^<!doctype html|^<html\b/i.test(trimmed)) {
    const afterHead = trimmed.split(/<\/head>/i)[1] ?? "";
    const bodySection = afterHead.split(/<\/html>/i)[0] ?? afterHead;
    return (
      VISIBLE_HTML_PATTERN.test(bodySection) ||
      stripHtmlTags(bodySection).length > 0
    );
  }

  return (
    VISIBLE_HTML_PATTERN.test(trimmed) || stripHtmlTags(trimmed).length > 0
  );
}

function buildPreviewDocument(code: string) {
  const trimmed = code.trim();
  if (!trimmed) {
    return "";
  }

  let documentHtml = trimmed;
  if (!/^<!doctype html/i.test(trimmed) && !/^<html\b/i.test(trimmed)) {
    documentHtml = `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body>${trimmed}</body></html>`;
  }

  return documentHtml;
}

const INLINE_PREVIEW_MIN_HEIGHT = 240;
const INLINE_PREVIEW_MAX_HEIGHT = 560;

function resizeHtmlPreview(iframe: HTMLIFrameElement) {
  try {
    const doc = iframe.contentDocument;
    if (!doc?.documentElement) {
      return;
    }

    const contentHeight = Math.max(
      doc.documentElement.scrollHeight,
      doc.body?.scrollHeight ?? 0,
      INLINE_PREVIEW_MIN_HEIGHT,
    );
    iframe.style.height = `${Math.min(contentHeight, INLINE_PREVIEW_MAX_HEIGHT)}px`;
  } catch {
    iframe.style.height = "240px";
  }
}

function observeHtmlPreviewSize(iframe: HTMLIFrameElement) {
  const doc = iframe.contentDocument;
  if (!doc?.documentElement) {
    return () => undefined;
  }

  const handleContentLoad = () => resizeHtmlPreview(iframe);
  resizeHtmlPreview(iframe);
  doc.addEventListener("load", handleContentLoad, true);

  if (typeof ResizeObserver === "undefined") {
    return () => doc.removeEventListener("load", handleContentLoad, true);
  }

  const observer = new ResizeObserver(handleContentLoad);
  observer.observe(doc.documentElement);
  if (doc.body) {
    observer.observe(doc.body);
  }

  return () => {
    observer.disconnect();
    doc.removeEventListener("load", handleContentLoad, true);
  };
}

function getHtmlFilename(code: string) {
  const rawTitle = code.match(/<title\b[^>]*>([^<]+)<\/title>/i)?.[1]?.trim();
  const safeTitle = rawTitle
    ?.replace(/[\\/:*?"<>|]/g, "-")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 80);

  return `${safeTitle || "preview"}.html`;
}

const HtmlSource = ({ code }: { code: string }) => {
  const highlighted = useMemo(() => highlightCode(code, "markup"), [code]);

  return (
    <pre className="md-code-source">
      {highlighted ? (
        <code
          className="language-html"
          dangerouslySetInnerHTML={{ __html: highlighted }}
        />
      ) : (
        <code className="language-html">{code}</code>
      )}
    </pre>
  );
};

const HtmlPreview = ({
  code,
  iframeRef,
}: {
  code: string;
  iframeRef: RefObject<HTMLIFrameElement>;
}) => {
  const { t } = useTranslation();
  const previewDocument = useMemo(() => buildPreviewDocument(code), [code]);

  useEffect(() => {
    const iframe = iframeRef.current;
    if (!iframe) {
      return;
    }

    let stopObserving: () => void = () => undefined;
    const handleLoad = () => {
      stopObserving();
      stopObserving = observeHtmlPreviewSize(iframe);
    };

    iframe.addEventListener("load", handleLoad);
    if (iframe.contentDocument?.readyState === "complete") {
      handleLoad();
    }
    return () => {
      stopObserving();
      iframe.removeEventListener("load", handleLoad);
    };
  }, [previewDocument, iframeRef]);

  return (
    <div className="md-html-preview">
      <iframe
        ref={iframeRef}
        className="md-html-preview-iframe"
        sandbox="allow-same-origin"
        srcDoc={previewDocument}
        title={t("chat.markdownHtmlPreview")}
      />
    </div>
  );
};

const HtmlBlockComponent = ({
  code,
  isStreaming = false,
}: {
  code: string;
  isStreaming?: boolean;
}) => {
  const { t } = useTranslation();
  const [activeView, setActiveView] = useState<HtmlView>("preview");
  const [copyStatus, setCopyStatus] = useState<CopyStatus>("idle");
  const [workspaceHost, setWorkspaceHost] = useState<HTMLElement | null>(null);
  const previewIframeRef = useRef<HTMLIFrameElement>(null);
  const fullscreenTriggerRef = useRef<HTMLButtonElement>(null);
  const workspaceRef = useRef<HTMLElement>(null);
  const workspaceCloseButtonRef = useRef<HTMLButtonElement>(null);
  const copyResetTimerRef = useRef<number | null>(null);

  const canShowPreview = hasRenderableHtml(code);
  const canCopySource = Boolean(code.trim());
  const canDownload = canCopySource && !isStreaming;
  const previewDocument = useMemo(() => buildPreviewDocument(code), [code]);
  const filename = useMemo(
    () => getHtmlFilename(isStreaming ? "" : code),
    [code, isStreaming],
  );

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

  useEffect(() => {
    if (!workspaceHost) {
      return;
    }

    const activeBeforeOpen =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    workspaceCloseButtonRef.current?.focus();

    const workspaceElement = workspaceRef.current;
    const siblingStates = Array.from(workspaceHost.children)
      .filter((element): element is HTMLElement =>
        element instanceof HTMLElement && element !== workspaceElement,
      )
      .map((element) => ({
        element,
        hadInert: element.hasAttribute("inert"),
        previousAriaHidden: element.getAttribute("aria-hidden"),
      }));

    siblingStates.forEach(({ element }) => {
      element.setAttribute("inert", "");
      element.setAttribute("aria-hidden", "true");
    });

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") {
        return;
      }
      event.preventDefault();
      setWorkspaceHost(null);
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      siblingStates.forEach(
        ({ element, hadInert, previousAriaHidden }) => {
          if (!hadInert) {
            element.removeAttribute("inert");
          }
          if (previousAriaHidden === null) {
            element.removeAttribute("aria-hidden");
          } else {
            element.setAttribute("aria-hidden", previousAriaHidden);
          }
        },
      );

      const focusTarget = fullscreenTriggerRef.current?.isConnected
        ? fullscreenTriggerRef.current
        : activeBeforeOpen?.isConnected
          ? activeBeforeOpen
          : null;
      focusTarget?.focus();
    };
  }, [workspaceHost]);

  useEffect(() => {
    if (isStreaming || !canShowPreview) {
      setWorkspaceHost(null);
    }
  }, [canShowPreview, isStreaming]);

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

  const handleDownload = () => {
    if (!canDownload) {
      return;
    }
    downloadStream(
      new Blob([code], { type: "text/html;charset=utf-8" }),
      filename,
    );
  };

  const handleOpenWorkspace = () => {
    const mainContent = fullscreenTriggerRef.current?.closest(
      ".main-layout-body",
    );
    setWorkspaceHost(
      mainContent instanceof HTMLElement ? mainContent : document.body,
    );
  };

  const renderContent = () => {
    if (activeView === "source") {
      return <HtmlSource code={code} />;
    }
    if (isStreaming) {
      return (
        <div className="md-html-generating" aria-live="polite">
          <FileTextOutlined className="md-html-generating-icon" />
          <div className="md-html-generating-content">
            <span>{t("chat.markdownHtmlGenerating")}</span>
            <div className="md-html-skeleton" aria-hidden="true">
              <i />
              <i />
              <i />
            </div>
          </div>
        </div>
      );
    }
    if (canShowPreview) {
      return <HtmlPreview code={code} iframeRef={previewIframeRef} />;
    }
    return (
      <div className="md-html-empty" role="status">
        <CodeOutlined />
        <span>{t("chat.markdownHtmlPreviewUnavailable")}</span>
      </div>
    );
  };

  return (
    <section className="md-html-block" aria-label={t("chat.markdownHtmlDisplay")}>
      <div className="md-html-toolbar">
        <div className="md-html-file" title={filename}>
          <FileTextOutlined className="md-html-file-icon" aria-hidden="true" />
          <span className="md-html-file-name">{filename}</span>
          <span className="md-html-file-type">HTML</span>
        </div>

        <div
          className="md-html-actions"
          role="group"
          aria-label={t("chat.markdownHtmlActions")}
        >
          <Tooltip title={t("chat.markdownRender")}>
            <button
              aria-label={t("chat.markdownRender")}
              aria-pressed={activeView === "preview"}
              className={activeView === "preview" ? "active" : ""}
              disabled={isStreaming || !canShowPreview}
              type="button"
              onClick={() => setActiveView("preview")}
            >
              <PlayCircleOutlined />
            </button>
          </Tooltip>
          <Tooltip title={t("chat.markdownSource")}>
            <button
              aria-label={t("chat.markdownSource")}
              aria-pressed={activeView === "source"}
              className={activeView === "source" ? "active" : ""}
              disabled={!canCopySource}
              type="button"
              onClick={() => setActiveView("source")}
            >
              <CodeOutlined />
            </button>
          </Tooltip>
          {activeView === "source" && (
            <Tooltip title={t(getCopyTooltip(copyStatus))}>
              <button
                aria-label={t("chat.markdownCopySource")}
                className={copyStatus === "copied" ? "copied" : ""}
                disabled={!canCopySource || copyStatus === "copying"}
                type="button"
                onClick={handleCopySource}
              >
                {copyStatus === "copied" ? (
                  <CheckOutlined />
                ) : (
                  <CopyOutlined />
                )}
              </button>
            </Tooltip>
          )}
          <Tooltip title={t("chat.markdownHtmlDownload")}>
            <button
              aria-label={t("chat.markdownHtmlDownload")}
              disabled={!canDownload}
              type="button"
              onClick={handleDownload}
            >
              <DownloadOutlined />
            </button>
          </Tooltip>
          <Tooltip title={t("chat.markdownEnlargePreview")}>
            <button
              ref={fullscreenTriggerRef}
              aria-label={t("chat.markdownEnlargePreview")}
              disabled={isStreaming || !canShowPreview}
              type="button"
              onClick={handleOpenWorkspace}
            >
              <FullscreenOutlined />
            </button>
          </Tooltip>
          <span className="md-mermaid-copy-status" aria-live="polite">
            {getCopyAnnouncement(copyStatus)
              ? t(getCopyAnnouncement(copyStatus))
              : ""}
          </span>
        </div>
      </div>

      {renderContent()}

      {workspaceHost &&
        createPortal(
          <section
            ref={workspaceRef}
            aria-label={`${t("chat.markdownHtmlFullscreenPreview")}: ${filename}`}
            aria-modal="false"
            className={`rag-markdown md-html-workspace${
              workspaceHost.tagName === "BODY"
                ? " md-html-workspace--viewport"
                : ""
            }`}
            role="dialog"
          >
            <header className="md-html-workspace-header">
              <div className="md-html-workspace-primary-actions">
                <Tooltip title={t("chat.markdownCloseFullscreen")}>
                  <button
                    ref={workspaceCloseButtonRef}
                    aria-label={t("chat.markdownCloseFullscreen")}
                    className="md-html-workspace-icon-button"
                    type="button"
                    onClick={() => setWorkspaceHost(null)}
                  >
                    <CloseOutlined />
                  </button>
                </Tooltip>
                <span className="md-html-workspace-divider" aria-hidden="true" />
                <button
                  aria-label={
                    activeView === "preview"
                      ? t("chat.markdownShowSource")
                      : t("chat.markdownShowPreview")
                  }
                  className="md-html-workspace-view-button"
                  type="button"
                  onClick={() =>
                    setActiveView((view) =>
                      view === "preview" ? "source" : "preview",
                    )
                  }
                >
                  {activeView === "preview" ? (
                    <CodeOutlined />
                  ) : (
                    <PlayCircleOutlined />
                  )}
                  <span>
                    {activeView === "preview"
                      ? t("chat.markdownShowSource")
                      : t("chat.markdownShowPreview")}
                  </span>
                </button>
              </div>

              <div
                className="md-html-workspace-secondary-actions"
                role="group"
                aria-label={t("chat.markdownHtmlFullscreenActions")}
              >
                <Tooltip title={t(getCopyTooltip(copyStatus))}>
                  <button
                    aria-label={t("chat.markdownCopySource")}
                    className={`md-html-workspace-icon-button${
                      copyStatus === "copied" ? " copied" : ""
                    }`}
                    disabled={!canCopySource || copyStatus === "copying"}
                    type="button"
                    onClick={handleCopySource}
                  >
                    {copyStatus === "copied" ? (
                      <CheckOutlined />
                    ) : (
                      <CopyOutlined />
                    )}
                  </button>
                </Tooltip>
                <Tooltip title={t("chat.markdownHtmlDownload")}>
                  <button
                    aria-label={t("chat.markdownHtmlDownload")}
                    className="md-html-workspace-icon-button"
                    disabled={!canDownload}
                    type="button"
                    onClick={handleDownload}
                  >
                    <DownloadOutlined />
                  </button>
                </Tooltip>
                <span className="md-mermaid-copy-status" aria-live="polite">
                  {getCopyAnnouncement(copyStatus)
                    ? t(getCopyAnnouncement(copyStatus))
                    : ""}
                </span>
              </div>
            </header>

            <div className="md-html-workspace-content">
              {activeView === "source" ? (
                <div className="md-html-workspace-source">
                  <HtmlSource code={code} />
                </div>
              ) : (
                <div className="md-html-workspace-preview-shell">
                  <iframe
                    className="md-html-preview-iframe"
                    sandbox="allow-same-origin"
                    srcDoc={previewDocument}
                    title={t("chat.markdownHtmlFullscreenPreview")}
                  />
                </div>
              )}
            </div>
          </section>,
          workspaceHost,
        )}
    </section>
  );
};

const HtmlBlock = memo(HtmlBlockComponent);

export default HtmlBlock;
