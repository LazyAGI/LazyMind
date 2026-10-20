import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as XLSX from "xlsx";
import FileViewer from "./index";

const originalCreateObjectURL = URL.createObjectURL;
const originalRevokeObjectURL = URL.revokeObjectURL;

vi.mock("react-i18next", async (importOriginal) => ({
  ...await importOriginal<typeof import("react-i18next")>(),
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("@/i18n", () => ({ default: { t: (key: string) => key } }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getAuthHeaders: () => ({}) } }));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code }));
vi.mock("@/modules/knowledge/utils/request", () => ({ normalizeProxyableUrl: (url: string) => url }));
vi.mock("@/components/ui", () => ({
  RenderPdf: () => null, exportPdfAsImagePdf: vi.fn(), isLearningActionCompatible: () => false,
}));

beforeEach(() => {
  vi.spyOn(console, "log").mockImplementation(() => {});
  URL.createObjectURL = vi.fn(() => "blob:excel-fixture");
  URL.revokeObjectURL = vi.fn();
  // jsdom cannot paint a canvas; keep the real preview library, XLSX decoding,
  // grid events, and FileViewer integration while stubbing only drawing APIs.
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => new Proxy({
    measureText: (text: string) => ({ width: String(text).length * 7 }),
  }, { get: (target, key) => key in target ? target[key as keyof typeof target] : vi.fn() }) as unknown as CanvasRenderingContext2D);
  const workbook = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([
    ["Item", "Quantity", "Note"], ["Alpha", 12, "Confirmed"], ["Beta", 5, "Pending"],
  ]), "Orders");
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([
    ["Category", "Amount"], ["Travel", 0],
  ]), "Budget");
  const bytes = XLSX.write(workbook, { type: "array", bookType: "xlsx" }) as ArrayBuffer;
  vi.stubGlobal("XMLHttpRequest", class {
    status = 200;
    response = bytes;
    responseType = "arraybuffer";
    onload?: () => void;
    open() {}
    setRequestHeader() {}
    send() { queueMicrotask(() => this.onload?.()); }
  });
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, arrayBuffer: async () => bytes }));
});

afterEach(() => {
  window.getSelection()?.removeAllRanges();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  if (originalCreateObjectURL) URL.createObjectURL = originalCreateObjectURL;
  else Reflect.deleteProperty(URL, "createObjectURL");
  if (originalRevokeObjectURL) URL.revokeObjectURL = originalRevokeObjectURL;
  else Reflect.deleteProperty(URL, "revokeObjectURL");
});

async function openSpreadsheet(onAsk = vi.fn()) {
  const view = render(<FileViewer file="https://example.test/orders.xlsx" fileName="orders.xlsx"
    onPdfSelection={onAsk} translationConfigured={false} />);
  await waitFor(() => {
    expect(view.container.querySelector(".x-spreadsheet-overlayer")).not.toBeNull();
    expect(view.container.querySelector(".x-spreadsheet-bottombar .active")).toHaveTextContent("Orders");
    expect(screen.queryByText("knowledge.excelPreviewLoading")).not.toBeInTheDocument();
  });
  return { ...view, onAsk, grid: view.container.querySelector(".x-spreadsheet-overlayer")! };
}

function gridPointer(grid: Element, type: "mousedown" | "mousemove" | "mouseup", x: number, y: number) {
  const event = new MouseEvent(type, { bubbles: true, clientX: x, clientY: y, buttons: type === "mouseup" ? 0 : 1 });
  // jsdom has no element layout from which to calculate relative offsets.
  Object.defineProperties(event, { offsetX: { value: x }, offsetY: { value: y } });
  fireEvent(grid, event);
}

async function askAboutSelection() {
  const button = await screen.findByRole("button", { name: "knowledge.askPdfSelection" });
  fireEvent.mouseDown(button);
  fireEvent.mouseUp(button);
  fireEvent.click(button);
}

describe("real XLSX selection questions", () => {
  it("asks about a canvas cell without a DOM text selection and preserves row context", async () => {
    const { grid, onAsk } = await openSpreadsheet();
    gridPointer(grid, "mousedown", 100, 60);
    gridPointer(grid, "mouseup", 100, 60);
    expect(window.getSelection()?.toString()).toBe("");
    await askAboutSelection();
    expect(onAsk).toHaveBeenCalledWith({ text: "Alpha", page: 1,
      context: expect.stringContaining("Confirmed") });
    expect(onAsk.mock.calls[0][0].context).toContain("Item");
  });

  it("reads a dragged range and tracks the active worksheet", async () => {
    const { grid, onAsk } = await openSpreadsheet();
    gridPointer(grid, "mousedown", 100, 60);
    gridPointer(grid, "mousemove", 180, 84);
    gridPointer(grid, "mouseup", 180, 84);
    await askAboutSelection();
    expect(onAsk).toHaveBeenLastCalledWith({ text: "Alpha\t12\nBeta\t5", page: 1, context: expect.stringContaining("A2:B3") });
    fireEvent.click(screen.getAllByText("Budget", { exact: true }).find((element) => element.tagName === "LI")!);
    gridPointer(grid, "mousedown", 180, 60);
    gridPointer(grid, "mouseup", 180, 60);
    await askAboutSelection();
    expect(onAsk).toHaveBeenLastCalledWith({ text: "0", page: 2, context: expect.stringContaining("Travel") });
  });

  it("preserves the XLS HTML-table selection path", async () => {
    const workbook = XLSX.utils.book_new();
    XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([["Item"], ["Alpha"]]), "Orders");
    const bytes = XLSX.write(workbook, { type: "array", bookType: "xls" }) as ArrayBuffer;
    vi.mocked(fetch).mockResolvedValue({ ok: true, arrayBuffer: async () => bytes } as Response);
    const onAsk = vi.fn();
    render(<FileViewer file="https://example.test/orders.xls" fileName="orders.xls" onPdfSelection={onAsk} />);
    const cell = await screen.findByRole("cell", { name: "Alpha" });
    const range = document.createRange();
    range.selectNodeContents(cell);
    window.getSelection()?.addRange(range);
    fireEvent.mouseUp(cell, { clientX: 100, clientY: 60 });
    await askAboutSelection();
    expect(onAsk).toHaveBeenCalledWith({ text: "Alpha", context: "Alpha", page: 1 });
  });
});
