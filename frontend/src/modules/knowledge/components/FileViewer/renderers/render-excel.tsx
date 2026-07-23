import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import jsPreviewExcel, { JsExcelPreview } from "@js-preview/excel";
import { Segment } from "@/api/generated/knowledge-client";
import i18n from "@/i18n";

import * as XLSX from "xlsx";

type JsPreviewType = JsExcelPreview;

interface RenderOfficeProps {
  fileData: ArrayBuffer;
  fileType: "excel";
  content: Segment["content"] | null;
  metadata: Record<string, unknown> | null;
}

const RenderExcel = (props: RenderOfficeProps) => {
  const { fileData, fileType, content, metadata } = props;
  const reader = useRef<JsPreviewType | null>(null);
  const showFile = useRef<HTMLDivElement>(null);
  const [loading, setLoading] = useState(false);

  const contentText = useMemo(() => content || "", [content]);
  const metaTitle = useMemo(
    () => (metadata?.title as string) || "",
    [metadata],
  );

  const getReaderType = useCallback(() => {
    return jsPreviewExcel;
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

      const readerType = getReaderType();
      if (!readerType || !showFile.current) {
        return;
      }

      showFile.current.innerHTML = "";

      try {
        const ab = fileData as ArrayBuffer;
        const header = new Uint8Array(ab.slice(0, 4));
        const isOle =
          header[0] === 0xd0 &&
          header[1] === 0xcf &&
          header[2] === 0x11 &&
          header[3] === 0xe0;
        if (isOle) {
          try {
            const workbook = XLSX.read(new Uint8Array(ab), {
              type: "array",
              cellStyles: true,
            });
            const firstSheetName = workbook.SheetNames[0];
            const sheet = workbook.Sheets[firstSheetName];

            const escapeHtml = (str: any) => {
              if (str === undefined || str === null) {
                return "";
              }
              return String(str)
                .replace(/&/g, "&amp;")
                .replace(/</g, "&lt;")
                .replace(/>/g, "&gt;")
                .replace(/"/g, "&quot;")
                .replace(/'/g, "&#39;");
            };

            const sheetToStyledHtml = (sheet: any) => {
              const ref = sheet["!ref"] || "A1";
              const range = XLSX.utils.decode_range(ref);
              let html = '<div class="sheet-wrapper" style="overflow:auto;">';
              html +=
                '<table class="sheet-table" style="border-collapse:collapse; width:100%;">';

              for (let r = range.s.r; r <= range.e.r; ++r) {
                html += "<tr>";
                for (let c = range.s.c; c <= range.e.c; ++c) {
                  const addr = XLSX.utils.encode_cell({ r, c });
                  const cell = sheet[addr];
                  const rawText = cell ? (cell.w ?? cell.v ?? "") : "";

                  let style =
                    "border:1px solid #e6e6e6;padding:6px;vertical-align:top;";
                  const s = cell && cell.s ? cell.s : null;
                  if (s) {
                    if (s.font) {
                      if (s.font.bold) {
                        style += "font-weight:bold;";
                      }
                      if (s.font.italic) {
                        style += "font-style:italic;";
                      }
                      if (s.font.underline) {
                        style += "text-decoration:underline;";
                      }
                      if (s.font.sz) {
                        style += `font-size:${s.font.sz}px;`;
                      }
                      if (s.font.color && s.font.color.rgb) {
                        style += `color:#${s.font.color.rgb.replace(/^00/, "")};`;
                      }
                    }
                    if (s.fill && s.fill.fgColor && s.fill.fgColor.rgb) {
                      style += `background-color:#${s.fill.fgColor.rgb.replace(/^00/, "")};`;
                    }
                    if (s.alignment) {
                      if (s.alignment.horizontal) {
                        style += `text-align:${s.alignment.horizontal};`;
                      }
                      if (s.alignment.vertical) {
                        style += `vertical-align:${s.alignment.vertical};`;
                      }
                      if (s.alignment.wrapText) {
                        style += "white-space:normal;";
                      } else {
                        style += "white-space:nowrap;";
                      }
                    }
                  }

                  html += `<td style="${style}">${escapeHtml(rawText)}</td>`;
                }
                html += "</tr>";
              }

              html += "</table></div>";
              return html;
            };

            const html = sheetToStyledHtml(sheet);
            if (showFile.current) {
              showFile.current.innerHTML = html;
            }
            return;
          } catch (xlsErr) {
            console.error("Failed to parse .xls with SheetJS:", xlsErr);
          }
        }
      } catch (hdrErr) {
        console.warn("Header check failed:", hdrErr);
      }

      reader.current = readerType.init(showFile.current);
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

export default RenderExcel;
