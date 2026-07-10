import { CheckOutlined, CopyOutlined, FullscreenOutlined } from "@ant-design/icons";
import { Modal, Tooltip, message } from "antd";
import { memo, useEffect, useMemo, useRef, useState, type RefObject } from "react";

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
      throw new Error("Copy command failed");
    }
  } finally {
    document.body.removeChild(textarea);
  }
}

function getCopyTooltip(status: CopyStatus) {
  if (status === "copied") {
    return "已复制";
  }
  if (status === "failed") {
    return "复制失败";
  }
  return "复制源码";
}

function getCopyAnnouncement(status: CopyStatus) {
  if (status === "copied") {
    return "源码已复制";
  }
  if (status === "failed") {
    return "源码复制失败";
  }
  return "";
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
}: {
  code: string;
  iframeRef: RefObject<HTMLIFrameElement>;
}) => {
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
  }, [code, iframeRef]);

  return (
    <div className="md-html-preview">
      <iframe
        ref={iframeRef}
        className="md-html-preview-iframe"
        sandbox="allow-same-origin"
        srcDoc={code}
        title="HTML preview"
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
  const [activeView, setActiveView] = useState<HtmlView>("preview");
  const [copyStatus, setCopyStatus] = useState<CopyStatus>("idle");
  const [isModalOpen, setIsModalOpen] = useState(false);
  const previewIframeRef = useRef<HTMLIFrameElement>(null);
  const modalIframeRef = useRef<HTMLIFrameElement>(null);
  const copyResetTimerRef = useRef<number | null>(null);

  const canShowPreview = Boolean(code.trim());
  const canCopySource = Boolean(code.trim());

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
      message.success("源码已复制");
    } catch {
      setCopyStatus("failed");
      message.error("复制失败，请手动复制");
    } finally {
      resetCopyStatusLater();
    }
  };

  return (
    <div className="md-html-block">
      <div className="md-mermaid-toolbar">
        <div className="md-mermaid-tabs" role="tablist" aria-label="HTML展示">
          <button
            aria-selected={activeView === "preview"}
            className={activeView === "preview" ? "active" : ""}
            disabled={!canShowPreview && !isStreaming}
            role="tab"
            type="button"
            onClick={() => setActiveView("preview")}
          >
            渲染
          </button>
          <button
            aria-selected={activeView === "source"}
            className={activeView === "source" ? "active" : ""}
            role="tab"
            type="button"
            onClick={() => setActiveView("source")}
          >
            源码
          </button>
        </div>
        <div className="md-mermaid-actions">
          {canShowPreview && activeView === "preview" && (
            <button
              aria-label="放大预览"
              className="md-mermaid-icon-button"
              type="button"
              onClick={() => setIsModalOpen(true)}
            >
              <FullscreenOutlined />
            </button>
          )}
          {activeView === "source" && (
            <Tooltip title={getCopyTooltip(copyStatus)}>
              <button
                aria-label="复制源码"
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
            {getCopyAnnouncement(copyStatus)}
          </span>
        </div>
      </div>

      {isStreaming && !canShowPreview && (
        <div className="md-mermaid-status">HTML 生成中，等待完整内容...</div>
      )}

      {activeView === "source" ? (
        <HtmlSource code={code} />
      ) : canShowPreview ? (
        <HtmlPreview code={code} iframeRef={previewIframeRef} />
      ) : (
        <div className="md-mermaid-placeholder" aria-live="polite">
          HTML 生成中...
        </div>
      )}

      <Modal
        centered
        className="md-html-modal"
        footer={null}
        open={isModalOpen}
        title="HTML 预览"
        width="80vw"
        onCancel={() => setIsModalOpen(false)}
      >
        {canShowPreview && (
          <div className="md-html-modal-content">
            <iframe
              ref={modalIframeRef}
              className="md-html-preview-iframe"
              sandbox="allow-same-origin"
              srcDoc={code}
              title="HTML fullscreen preview"
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
