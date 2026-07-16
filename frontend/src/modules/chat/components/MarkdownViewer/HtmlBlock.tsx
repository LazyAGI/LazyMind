import { CheckOutlined, CopyOutlined, FullscreenOutlined, LoadingOutlined } from "@ant-design/icons";
import { Modal, Tooltip, message } from "antd";
import { memo, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";

import {
  DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
} from "@/utils/developerMode";
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

function stripBodyBackgroundFromHtml(html: string) {
  let result = html.replace(/<body\b([^>]*)>/gi, (_match, attrs: string) => {
    const cleanedAttrs = attrs.replace(
      /\sstyle\s*=\s*(["'])([\s\S]*?)\1/i,
      (_styleAttr: string, quote: string, style: string) => {
        const cleanedStyle = style
          .replace(
            /\bbackground(?:-image|-color|-size|-position|-repeat|-attachment)?\s*:[^;]+;?/gi,
            "",
          )
          .trim()
          .replace(/;\s*;/g, ";");
        return cleanedStyle ? ` style=${quote}${cleanedStyle}${quote}` : "";
      },
    );
    return `<body${cleanedAttrs}>`;
  });

  result = result.replace(
    /<style\b([^>]*)>([\s\S]*?)<\/style>/gi,
    (_match, attrs: string, css: string) => {
      const cleanedCss = css.replace(
        /body\s*\{([^}]*)\}/gi,
        (_bodyRule: string, declarations: string) => {
          const cleanedDeclarations = declarations
            .replace(
              /\bbackground(?:-image|-color|-size|-position|-repeat|-attachment)?\s*:[^;]+;?/gi,
              "",
            )
            .trim()
            .replace(/;\s*;/g, ";");
          return cleanedDeclarations
            ? `body { ${cleanedDeclarations} }`
            : "body {}";
        },
      );
      return `<style${attrs}>${cleanedCss}</style>`;
    },
  );

  return result;
}

const PREVIEW_BODY_BG_RESET =
  '<style data-lazymind-preview-reset>html,body{background:transparent!important;background-image:none!important;}</style>';

function injectPreviewBodyBackgroundReset(html: string) {
  if (/<\/head>/i.test(html)) {
    return html.replace(/<\/head>/i, `${PREVIEW_BODY_BG_RESET}</head>`);
  }
  if (/<body\b/i.test(html)) {
    return html.replace(/<body\b/i, `${PREVIEW_BODY_BG_RESET}<body`);
  }
  return `${PREVIEW_BODY_BG_RESET}${html}`;
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

  documentHtml = stripBodyBackgroundFromHtml(documentHtml);
  return injectPreviewBodyBackgroundReset(documentHtml);
}

function resizeHtmlPreview(iframe: HTMLIFrameElement) {
  try {
    const doc = iframe.contentDocument;
    if (!doc?.documentElement) {
      return;
    }

    const height = Math.max(
      doc.documentElement.scrollHeight,
      doc.body?.scrollHeight ?? 0,
      160,
    );
    iframe.style.height = `${height}px`;
  } catch {
    iframe.style.height = "240px";
  }
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
  inline = false,
}: {
  code: string;
  iframeRef: RefObject<HTMLIFrameElement>;
  inline?: boolean;
}) => {
  const { t } = useTranslation();
  const previewDocument = useMemo(() => buildPreviewDocument(code), [code]);

  useEffect(() => {
    const iframe = iframeRef.current;
    if (!iframe) {
      return;
    }

    const handleLoad = () => {
      resizeHtmlPreview(iframe);
    };

    iframe.addEventListener("load", handleLoad);
    return () => iframe.removeEventListener("load", handleLoad);
  }, [previewDocument, iframeRef]);

  return (
    <div className={`md-html-preview${inline ? " md-html-preview--inline" : ""}`}>
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
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const previewIframeRef = useRef<HTMLIFrameElement>(null);
  const modalIframeRef = useRef<HTMLIFrameElement>(null);
  const copyResetTimerRef = useRef<number | null>(null);

  const canShowPreview = hasRenderableHtml(code);
  const isGenerating =
    !canShowPreview && (isStreaming || Boolean(code.trim()));
  const canCopySource = Boolean(code.trim());
  const previewDocument = useMemo(() => buildPreviewDocument(code), [code]);

  useEffect(() => {
    const syncDeveloperActive = () => {
      setDeveloperActive(isDeveloperModeActive());
    };

    const handleDeveloperActiveChange = (event: Event) => {
      const nextActive = (event as CustomEvent<{ active?: boolean }>).detail
        ?.active;
      setDeveloperActive(
        typeof nextActive === "boolean" ? nextActive : isDeveloperModeActive(),
      );
    };

    window.addEventListener("storage", syncDeveloperActive);
    window.addEventListener(DEVELOPER_ACTIVE_EVENT, handleDeveloperActiveChange);

    return () => {
      window.removeEventListener("storage", syncDeveloperActive);
      window.removeEventListener(
        DEVELOPER_ACTIVE_EVENT,
        handleDeveloperActiveChange,
      );
    };
  }, []);

  useEffect(() => {
    if (!developerActive && activeView === "source") {
      setActiveView("preview");
    }
  }, [activeView, developerActive]);

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

  const renderContent = () => {
    if (developerActive && activeView === "source") {
      return <HtmlSource code={code} />;
    }
    if (canShowPreview) {
      return (
        <HtmlPreview
          code={code}
          iframeRef={previewIframeRef}
          inline={!developerActive}
        />
      );
    }
    if (isGenerating) {
      return (
        <div
          className={
            developerActive
              ? "md-html-generating md-html-generating--card"
              : "md-html-generating"
          }
          aria-live="polite"
        >
          <LoadingOutlined spin className="md-html-generating-icon" />
          <span>{t("chat.markdownHtmlGenerating")}</span>
        </div>
      );
    }
    return null;
  };

  return (
    <div
      className={`md-html-block${
        developerActive ? "" : " md-html-block--inline"
      }`}
    >
      {developerActive && (
        <div className="md-mermaid-toolbar">
          <div className="md-mermaid-tabs" role="tablist" aria-label={t("chat.markdownHtmlDisplay")}>
            <button
              aria-selected={activeView === "preview"}
              className={activeView === "preview" ? "active" : ""}
              disabled={!canShowPreview && !isStreaming}
              role="tab"
              type="button"
              onClick={() => setActiveView("preview")}
            >
              {t("chat.markdownRender")}
            </button>
            <button
              aria-selected={activeView === "source"}
              className={activeView === "source" ? "active" : ""}
              role="tab"
              type="button"
              onClick={() => setActiveView("source")}
            >
              {t("chat.markdownSource")}
            </button>
          </div>
          <div className="md-mermaid-actions">
            {canShowPreview && activeView === "preview" && (
              <button
                aria-label={t("chat.markdownEnlargePreview")}
                className="md-mermaid-icon-button"
                type="button"
                onClick={() => setIsModalOpen(true)}
              >
                <FullscreenOutlined />
              </button>
            )}
            {activeView === "source" && (
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
                  {copyStatus === "copied" ? (
                    <CheckOutlined />
                  ) : (
                    <CopyOutlined />
                  )}
                </button>
              </Tooltip>
            )}
            <span className="md-mermaid-copy-status" aria-live="polite">
              {getCopyAnnouncement(copyStatus) ? t(getCopyAnnouncement(copyStatus)) : ""}
            </span>
          </div>
        </div>
      )}

      {renderContent()}

      <Modal
        centered
        className="md-html-modal"
        footer={null}
        open={isModalOpen}
        title={t("chat.markdownHtmlPreview")}
        width="80vw"
        onCancel={() => setIsModalOpen(false)}
      >
        {canShowPreview && (
          <div className="md-html-modal-content">
            <iframe
              ref={modalIframeRef}
              className="md-html-preview-iframe"
              sandbox="allow-same-origin"
              srcDoc={previewDocument}
              title={t("chat.markdownHtmlFullscreenPreview")}
              onLoad={() => {
                if (modalIframeRef.current) {
                  resizeHtmlPreview(modalIframeRef.current);
                }
              }}
            />
          </div>
        )}
      </Modal>
    </div>
  );
};

const HtmlBlock = memo(HtmlBlockComponent);

export default HtmlBlock;
