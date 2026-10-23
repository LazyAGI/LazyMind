import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import jsPreviewExcel, { JsExcelPreview } from "@js-preview/excel";
import { Segment } from "@/api/generated/knowledge-client";
import i18n from "@/i18n";

import * as XLSX from "xlsx";

// The pinned preview library exposes selection hooks and sheetIndex at runtime.
type JsPreviewType = JsExcelPreview & { sheetIndex?: number };
type ExcelSheet = { name: string; rows: Record<number, { cells: Record<number, { text?: string | number | boolean }> }> };
type ExcelRange = { startRowIndex: number; startColumnIndex: number; endRowIndex: number; endColumnIndex: number };
export type ExcelSelection = { text: string; context: string; page: number };

function hasRenderableCells(workbookSheets: ExcelSheet[]): boolean {
  return workbookSheets.some((sheet) =>
    Object.values(sheet.rows || {}).some((row) =>
      Object.values(row.cells || {}).some((cell) =>
        cell.text !== undefined && cell.text !== null && String(cell.text) !== "",
      ),
    ),
  );
}

function previewMatchesWorkbook(fileData: ArrayBuffer, previewSheets: ExcelSheet[]): boolean {
  if (!hasRenderableCells(previewSheets)) {
    return false;
  }

  try {
    const workbook = XLSX.read(new Uint8Array(fileData), {
      type: "array",
      cellDates: true,
    });
    const sourceSheets = workbook.SheetNames.filter((sheetName) => {
      const sheet = workbook.Sheets[sheetName];
      return XLSX.utils.sheet_to_json<unknown[]>(sheet, { header: 1, defval: "" })
        .some((row) => row.some((value) => value !== "" && value !== null && value !== undefined));
    });
    const renderedNames = previewSheets.map((sheet) => sheet.name);
    return sourceSheets.length > 0 &&
      sourceSheets.length === renderedNames.length &&
      sourceSheets.every((sheetName, index) => sheetName === renderedNames[index]);
  } catch (error) {
    console.warn("Failed to validate XLSX preview data:", error);
    return false;
  }
}

function normalizeXlsxForPreview(fileData: ArrayBuffer): ArrayBuffer | null {
  try {
    const workbook = XLSX.read(new Uint8Array(fileData), {
      type: "array",
      cellDates: true,
      cellStyles: true,
    });
    const normalized = XLSX.write(workbook, {
      type: "array",
      bookType: "xlsx",
      cellStyles: true,
    }) as ArrayBuffer | Uint8Array;
    if (normalized instanceof ArrayBuffer) {
      return normalized;
    }
    return normalized.buffer.slice(
      normalized.byteOffset,
      normalized.byteOffset + normalized.byteLength,
    ) as ArrayBuffer;
  } catch (error) {
    console.warn("Failed to normalize XLSX preview data:", error);
    return null;
  }
}

function renderWorkbookTableFallback(
  container: HTMLDivElement,
  fileData: ArrayBuffer,
): boolean {
  try {
    const workbook = XLSX.read(new Uint8Array(fileData), {
      type: "array",
      cellDates: true,
      cellStyles: true,
    });
    if (workbook.SheetNames.length === 0) {
      return false;
    }

    const root = document.createElement("div");
    root.className = "excel-table-fallback";
    const panes = document.createElement("div");
    panes.className = "excel-table-fallback-panes";
    const tabs = document.createElement("div");
    tabs.className = "excel-table-fallback-tabs";
    const paneElements: HTMLDivElement[] = [];
    const tabButtons: HTMLButtonElement[] = [];

    const activateSheet = (activeIndex: number) => {
      paneElements.forEach((pane, index) => {
        pane.hidden = index !== activeIndex;
      });
      tabButtons.forEach((button, index) => {
        button.classList.toggle("active", index === activeIndex);
      });
    };

    workbook.SheetNames.forEach((sheetName, sheetIndex) => {
      const sheet = workbook.Sheets[sheetName];
      const rows = XLSX.utils.sheet_to_json<(string | number | boolean)[]>(sheet, {
        header: 1,
        raw: false,
        defval: "",
      });
      const pane = document.createElement("div");
      pane.className = "excel-table-fallback-pane";
      const table = document.createElement("table");
      table.className = "excel-table-fallback-grid";

      rows.forEach((row) => {
        const tr = document.createElement("tr");
        row.forEach((value) => {
          const td = document.createElement("td");
          td.textContent = String(value ?? "");
          tr.appendChild(td);
        });
        table.appendChild(tr);
      });
      pane.appendChild(table);
      panes.appendChild(pane);
      paneElements.push(pane);

      const tab = document.createElement("button");
      tab.type = "button";
      tab.textContent = sheetName;
      tab.addEventListener("click", () => activateSheet(sheetIndex));
      tabs.appendChild(tab);
      tabButtons.push(tab);
    });

    root.appendChild(panes);
    root.appendChild(tabs);
    container.replaceChildren(root);
    activateSheet(0);
    return true;
  } catch (error) {
    console.warn("Failed to render XLSX table fallback:", error);
    return false;
  }
}

function requiresTableRenderer(fileData: ArrayBuffer): boolean {
  try {
    const workbook = XLSX.read(new Uint8Array(fileData), { type: "array" });
    return !workbook.Props?.Application && !workbook.Props?.Creator;
  } catch {
    return false;
  }
}

interface RenderOfficeProps {
  fileData: ArrayBuffer;
  onPreviewError?: () => void;
  fileType: "excel";
  content: Segment["content"] | null;
  metadata: Record<string, unknown> | null;
  onSelection?: (selection: ExcelSelection | null, point: Pick<MouseEvent, "clientX" | "clientY">) => void;
}

const RenderExcel = (props: RenderOfficeProps) => {
  const { fileData, fileType, content, metadata } = props;
  const errorCallback = useRef(props.onPreviewError);
  errorCallback.current = props.onPreviewError;
  const reader = useRef<JsPreviewType | null>(null);
  const showFile = useRef<HTMLDivElement>(null);
  const [loading, setLoading] = useState(false);
  const sheets = useRef<ExcelSheet[]>([]);
  const selectedRange = useRef<ExcelRange | null>(null);

  const handleCellSelection = (event: React.MouseEvent<HTMLDivElement>) => {
    if (!(event.target instanceof Element) || !event.target.closest(".vue-office-excel")) return;
    const range = selectedRange.current;
    const page = (reader.current?.sheetIndex ?? 0) + 1;
    const sheet = sheets.current[page - 1];
    if (!range || !sheet || range.startRowIndex < 0 || range.startColumnIndex < 0) {
      props.onSelection?.(null, event);
      return;
    }
    const rowIndexes = Object.keys(sheet.rows).map(Number).filter(Number.isInteger).sort((a, b) => a - b);
    const rows = rowIndexes.filter((row) => row >= range.startRowIndex && row <= range.endRowIndex);
    const contextRows = [...new Set([rowIndexes[0], ...rows])].filter((row) => row !== undefined);
    const columnIndexes = [...new Set(contextRows.flatMap((row) => Object.keys(sheet.rows[row].cells).map(Number)))].sort((a, b) => a - b);
    const columns = columnIndexes.filter((column) => column >= range.startColumnIndex && column <= range.endColumnIndex);
    const rowText = (row: number, cols: number[]) => cols.map((col) => String(sheet.rows[row]?.cells[col]?.text ?? "")).join("\t");
    const text = rows.map((row) => rowText(row, columns)).join("\n").trim();
    const address = XLSX.utils.encode_range({
      s: { r: range.startRowIndex, c: range.startColumnIndex },
      e: { r: range.endRowIndex, c: range.endColumnIndex },
    });
    props.onSelection?.(text ? {
      text, page, context: [sheet.name, address, ...contextRows.map((row) => rowText(row, columnIndexes))].join("\n"),
    } : null, event);
  };

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
      sheets.current = [];
      selectedRange.current = null;

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

        // Render minimal-metadata XLSX workbooks from the source bytes. The canvas
        // preview library can resolve successfully and then asynchronously
        // replace valid workbooks with an empty synthetic sheet ("sheet4").
        // A SheetJS-backed table is deterministic and keeps every worksheet.
        if (requiresTableRenderer(fileData) && renderWorkbookTableFallback(showFile.current, fileData)) {
          return;
        }
      } catch (hdrErr) {
        console.warn("Header check failed:", hdrErr);
      }

      const options = {
        showContextmenu: false,
        transformData: (data: ExcelSheet[]) => { sheets.current = data; return data; },
        cellSelected: ({ rowIndex, columnIndex }: { rowIndex: number; columnIndex: number }) => {
          selectedRange.current = { startRowIndex: rowIndex, endRowIndex: rowIndex, startColumnIndex: columnIndex, endColumnIndex: columnIndex };
        },
        cellsSelected: (range: ExcelRange) => { selectedRange.current = range; },
      };
      const initReader = () => readerType.init(showFile.current!, options) as JsPreviewType;
      reader.current = initReader();
      if (reader.current?.preview) {
        await reader.current.preview(fileData);

        // Some valid XLSX producers emit workbook metadata that the pinned
        // preview library accepts without throwing but renders as an empty
        // grid. Re-serializing through SheetJS keeps the workbook values and
        // sheets while giving the canvas preview a canonical XLSX package.
        if (!previewMatchesWorkbook(fileData, sheets.current)) {
          const normalized = normalizeXlsxForPreview(fileData);
          if (normalized) {
            reader.current.destroy?.();
            showFile.current.innerHTML = "";
            sheets.current = [];
            selectedRange.current = null;
            reader.current = initReader();
            await reader.current.preview(normalized);
            if (!previewMatchesWorkbook(fileData, sheets.current)) {
              reader.current.destroy?.();
              reader.current = null;
              renderWorkbookTableFallback(showFile.current, fileData);
            }
          } else {
            reader.current.destroy?.();
            reader.current = null;
            renderWorkbookTableFallback(showFile.current, fileData);
          }
        }
      }
    } catch (err) {
      if (showFile.current) errorCallback.current?.();
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
    <div className="file-viewer-container" onMouseUp={handleCellSelection}
      onMouseDownCapture={(event) => {
        if (event.target instanceof Element && event.target.closest(".x-spreadsheet-bottombar")) selectedRange.current = null;
      }}>
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
