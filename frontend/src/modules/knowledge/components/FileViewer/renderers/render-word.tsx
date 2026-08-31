import { useEffect, useRef, useState } from "react";
import { renderAsync } from "docx-preview";
import { Segment } from "@/api/generated/knowledge-client";
import i18n from "@/i18n";
import { Spin } from "antd";
import "../index.scss";

interface WordViewerProps {
  fileData: ArrayBuffer;
  content: Segment["content"] | null;
}

const RenderWord = (props: WordViewerProps) => {
  const { fileData, content } = props;
  const [loading, setLoading] = useState(true);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let isMounted = true;

    const loadDocx = async () => {
      setLoading(true);
      try {
        if (!containerRef.current) {
          return;
        }
        const container = containerRef.current;
        if (container) {
          container.innerHTML = "";
        }

        if (container && isMounted && fileData instanceof ArrayBuffer) {
          await renderAsync(fileData, container, undefined, {
            className: "docx",
            inWrapper: true,
          });
        }
      } catch (err) {
        console.error(i18n.t("knowledge.previewLoadFailedLog"), err);
      } finally {
        if (isMounted) {
          setLoading(false);
        }
      }
    };

    loadDocx();

    return () => {
      isMounted = false;
      const container = containerRef.current;
      if (container) {
        container.innerHTML = "";
      }
    };
  }, [fileData]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    clearHighlight(container);

    if (content?.trim()) {
      highlightKeyword(container, content);
      scrollToFirstMatch(container);
    }
  }, [content]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const handleClick = (e: MouseEvent) => {
      const target = e.target as HTMLAnchorElement;
      if (
        (target.tagName === "A" &&
          target.getAttribute("href")?.startsWith("#")) ||
        ((target.parentNode as Element)?.tagName === "A" &&
          (target.parentNode as Element)?.getAttribute("href")?.startsWith("#"))
      ) {
        e.preventDefault();
        const id =
          target?.getAttribute("href")?.slice(1) ||
          (target.parentNode as Element)?.getAttribute("href")?.slice(1);
        const el = container.querySelector(`[id="${id}"], [name="${id}"]`);
        if (el) {
          el.scrollIntoView({ behavior: "smooth" });
        }
      }
    };

    container.addEventListener("click", handleClick);
    return () => container.removeEventListener("click", handleClick);
  }, []);

  return (
    <div className="file-viewer-word-scroll">
      {loading && (
        <div
          style={{
            minHeight: 300,
            display: "flex",
            justifyContent: "center",
            alignItems: "center",
            border: "1px solid #ddd",
            backgroundColor: "#fff",
          }}
        >
          <Spin />
        </div>
      )}
      <div
        ref={containerRef}
        className="file-viewer-word-document"
        style={{
          border: "1px solid #ddd",
          backgroundColor: "#fff",
          lineHeight: 1.6,
          boxSizing: "border-box",
          overflowX: "auto",
          whiteSpace: "normal",
          wordBreak: "break-word",
          minHeight: "auto",
        }}
      />
    </div>
  );
};

export default RenderWord;

function highlightKeyword(container: HTMLDivElement, keyword: string) {
  if (!container || !keyword || typeof keyword !== "string") {
    return;
  }

  const terms = keyword
    .trim()
    .split(/[\s,，]+/)
    .filter(Boolean)
    .map((term) => term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));

  if (terms.length === 0) {
    return;
  }

  const regex = new RegExp(`(${terms.join("|")})`, "gi");

  const walk = document.createTreeWalker(container, NodeFilter.SHOW_TEXT);
  const nodesToHighlight: Text[] = [];

  while (walk.nextNode()) {
    const node = walk.currentNode as Text;
    if (regex.test(node.nodeValue || "")) {
      nodesToHighlight.push(node);
    }
  }

  nodesToHighlight.forEach((textNode) => {
    const spanWrapper = document.createElement("span");
    spanWrapper.innerHTML = textNode.nodeValue!.replace(
      regex,
      '<span style="background: yellow; font-weight: bold;">$1</span>',
    );
    const parent = textNode.parentNode;
    if (parent) {
      parent.replaceChild(spanWrapper, textNode);
    }
  });
}

function scrollToFirstMatch(container: HTMLDivElement) {
  const el = container.querySelector('span[style*="background"]');
  if (el) {
    el.scrollIntoView({ behavior: "smooth", block: "center" });
  }
}

function clearHighlight(container: HTMLElement) {
  const highlights = container.querySelectorAll('span[style*="background"]');
  highlights.forEach((el) => {
    const parent = el.parentNode!;
    while (el.firstChild) {
      parent.insertBefore(el.firstChild, el);
    }
    parent.removeChild(el);
  });
}
