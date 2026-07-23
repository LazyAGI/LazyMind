import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import jsPreviewDocx, { JsDocxPreview } from "@js-preview/docx";
import "@js-preview/docx/lib/index.css";
import { Segment } from "@/api/generated/knowledge-client";
import i18n from "@/i18n";

declare const jsPreviewExcel: typeof import("@js-preview/excel").default;

type JsPreviewType = JsDocxPreview | import("@js-preview/excel").JsExcelPreview;

interface RenderOfficeProps {
  fileData: ArrayBuffer;
  fileType: "docx" | "excel";
  content: Segment["content"] | null;
  metadata: Record<string, unknown> | null;
}

const RenderOffice = (props: RenderOfficeProps) => {
  const { fileData, fileType, content, metadata } = props;
  const reader = useRef<JsPreviewType | null>(null);
  const showFile = useRef<HTMLDivElement>(null);
  const [loading, setLoading] = useState(false);

  const contentText = useMemo(() => content || "", [content]);
  const metaTitle = useMemo(
    () => (metadata?.title as string) || "",
    [metadata],
  );

  const getReaderType = useCallback(async () => {
    switch (fileType) {
      case "docx":
        return jsPreviewDocx;
      case "excel":
        return (window as unknown as { jsPreviewExcel: typeof jsPreviewExcel }).jsPreviewExcel;
      default:
        return null;
    }
  }, [fileType]);

  const highlightKeyword = useCallback(
    (container: HTMLDivElement, keyword: string, title = "") => {
      if (!container || !keyword || typeof keyword !== "string") {
        return;
      }

      clearHighlight(container);

      const elements = container.querySelectorAll(
        "span, p",
      ) as NodeListOf<HTMLElement>;

      const textsToMatch: string[] = [];
      if (title && typeof title === "string" && keyword.includes(title)) {
        textsToMatch.push(title);
        const remainingText = keyword
          .replace(title, "")
          .replace(/[\s\n]+/g, " ")
          .trim();
        if (remainingText) {
          textsToMatch.push(remainingText);
        }
      } else {
        textsToMatch.push(keyword.replace(/[\s\n]+/g, " ").trim());
      }

      textsToMatch.forEach((text) => {
        elements.forEach((element) => {
          if (element.innerText === text) {
            element.style.backgroundColor = "yellow";
            element.scrollIntoView({ behavior: "smooth", block: "center" });
          }
        });
      });
    },
    [],
  );

  const clearHighlight = useCallback((container: HTMLElement) => {
    const elements = container.querySelectorAll("*") as NodeListOf<HTMLElement>;
    elements.forEach((element) => {
      element.style.backgroundColor = "";
    });
  }, []);

  const previewFile = useCallback(async () => {
    if (!fileData) {
      return;
    }

    if (reader.current) {
      reader.current.destroy?.();
      reader.current = null;
    }

    try {
      setLoading(true);

      const readerType = await getReaderType();
      if (!readerType || !showFile.current) {
        return;
      }

      showFile.current.innerHTML = "";

      reader.current = readerType.init(showFile.current, {
        inWrapper: true,
        ignoreWidth: true,
        ignoreHeight: true,
        ignoreFonts: false,
      });

      if (reader.current && reader.current.preview) {
        await reader.current.preview(fileData);
      }
    } catch (err) {
      console.error("Office preview error:", err);
    } finally {
      setLoading(false);
    }
  }, [fileData, fileType, getReaderType]);

  useEffect(() => {
    if (fileData) {
      previewFile();
    }
  }, [fileData, previewFile]);

  useEffect(() => {
    if (!showFile.current || !contentText?.trim()) {
      return;
    }

    if (showFile.current.children.length > 0) {
      setTimeout(() => {
        if (showFile.current) {
          highlightKeyword(showFile.current, contentText, metaTitle);
        }
      }, 100);
    }
  }, [contentText, metaTitle, highlightKeyword]);

  useEffect(() => {
    return () => {
      if (reader.current) {
        reader.current.destroy?.();
        reader.current = null;
      }
    };
  }, []);

  return (
    <div className="file-viewer-container">
      <div ref={showFile} className="file-viewer-content"></div>
      {loading && (
        <div
          style={{
            position: "absolute",
            top: "50%",
            left: "50%",
            transform: "translate(-50%, -50%)",
            zIndex: 10,
          }}
        >
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              fontSize: "16px",
              color: "#666",
            }}
          >
            <div style={{ marginBottom: "12px" }}>
              {i18n.t("knowledge.excelPreviewLoading")}
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default RenderOffice;
