import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PdfCitationLayer } from "./PdfCitationLayer";
import { groupPdfCitationTargets } from "./pdfCitationReferences";
import type { createPdfCitationPreviewCache, PdfCitationPreview, PdfCitationPreviewDocument } from "./pdfCitationPreview";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, options?: { number?: string }) => `${key}${options?.number ? ` [${options.number}]` : ""}` }) }));
const targets = groupPdfCitationTargets([{ referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 8, bbox: [20, 60, 300, 84] }]);
const pdf = {} as PdfCitationPreviewDocument;
const rectDescriptor = Object.getOwnPropertyDescriptor(Range.prototype, "getClientRects");
beforeEach(() => {
  vi.useFakeTimers();
  Object.defineProperty(Range.prototype, "getClientRects", { configurable: true, value() { return [{ left: 10, right: 60, top: 10, bottom: 30, width: 50, height: 20 }]; } });
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  if (rectDescriptor) Object.defineProperty(Range.prototype, "getClientRects", rectDescriptor);
  else Reflect.deleteProperty(Range.prototype, "getClientRects");
  window.getSelection()?.removeAllRanges();
});
function setup(preview: Promise<PdfCitationPreview | undefined> | (() => Promise<PdfCitationPreview | undefined>) = Promise.resolve(undefined), rawText?: string) {
  const navigate = vi.fn();
  const cache = { get: vi.fn(() => typeof preview === "function" ? preview() : preview), dispose: vi.fn(), disposeDocument: vi.fn() } satisfies ReturnType<typeof createPdfCitationPreviewCache>;
  const props = { targets: rawText === undefined ? targets : groupPdfCitationTargets([{ referenceId: "r12", referenceKey: "12", rawText, kind: "download", page: 8, bbox: [20, 60, 300, 84] }]), getDocument: () => pdf, previewCache: cache, onNavigate: navigate };
  const view = render(<div data-testid="page"><div className="react-pdf__Page__textContent"><span>[</span><span>12]</span></div><a href="#native">Native</a><PdfCitationLayer {...props}/></div>);
  const page = screen.getByTestId("page");
  return { ...view, props, page, navigate, cache };
}
async function hover(page: HTMLElement) {
  fireEvent.pointerMove(page, { clientX: 20, clientY: 20, buttons: 0 });
  await act(async () => { vi.advanceTimersByTime(299); });
  expect(screen.queryByRole("dialog")).toBeNull();
  await act(async () => { vi.advanceTimersByTime(1); });
}
describe("citation preview interaction", () => {
  it.each([
    "Jimmy Lei Ba, Jamie Ryan Kiros, and Geoffrey E Hinton. Layer normalization. arXiv preprint arXiv:1607.06450, 2016.",
    "J. R. Smith, A. Doe, et al. 2024. A title.",
    "Alex Graves. Generating sequences with recurrent neural networks. arXiv preprint, 2013.",
    "Jimmy Lei Ba,\nJamie Ryan Kiros. Layer normalization. 2016.",
  ])("keeps the complete extracted reference in the hover card: %s", async rawText => {
    const { page } = setup(new Promise(() => {}), rawText);
    await hover(page);
    expect(screen.getByRole("dialog").querySelector(".pdf-citation-raw")?.textContent).toBe(rawText);
  });

  it("waits 300 ms, keeps the portal open while entering it, and never navigates on hover", async () => {
    const { page, navigate } = setup(new Promise(() => {}));
    await hover(page);
    const card = screen.getByRole("dialog", { name: "pdfCitation.title [12]" });
    expect(screen.getByText("Paper")).toBeInTheDocument();
    fireEvent.pointerLeave(page);
    act(() => { vi.advanceTimersByTime(200); });
    fireEvent.pointerEnter(card);
    act(() => { vi.advanceTimersByTime(300); });
    expect(card).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.pointerLeave(card);
    act(() => { vi.advanceTimersByTime(249); });
    expect(card).toBeInTheDocument();
    act(() => { vi.advanceTimersByTime(1); });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("keeps raw text when the crop fails and navigates from the jump button", async () => {
    const { page, navigate } = setup(() => Promise.reject(new Error("render failed")));
    await hover(page);
    expect(screen.getByText("Paper")).toBeInTheDocument();
    expect(screen.getByText("pdfCitation.unavailable")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "pdfCitation.jump [12]" }));
    expect(navigate).toHaveBeenCalledWith(targets[0]);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("replaces the pending crop asynchronously", async () => {
    let resolve!: (value: PdfCitationPreview) => void;
    const { page } = setup(new Promise(done => { resolve = done; }));
    await hover(page);
    expect(screen.getByRole("status")).toHaveTextContent("pdfCitation.loading");
    await act(async () => { resolve({ url: "blob:crop", width: 200, height: 80 }); });
    expect(screen.getByRole("img")).toHaveAttribute("src", "blob:crop");
    expect(screen.getByText("Paper")).toBeInTheDocument();
  });

  it.each(["Escape", "scroll", "source"])("closes on %s and ignores a late crop", async reason => {
    let resolve!: (value: PdfCitationPreview) => void;
    const view = setup(new Promise(done => { resolve = done; }));
    await hover(view.page);
    if (reason === "Escape") fireEvent.keyDown(document, { key: "Escape" });
    if (reason === "scroll") fireEvent.scroll(view.page);
    if (reason === "source") view.rerender(<div><PdfCitationLayer {...view.props} sourceKey="next"/></div>);
    await act(async () => { resolve({ url: "blob:late", width: 200, height: 80 }); });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("provides a separate keyboard entry and navigates only on activation", async () => {
    const { navigate } = setup();
    const trigger = screen.getByRole("button", { name: "pdfCitation.view [12]" });
    fireEvent.focus(trigger);
    await act(async () => {});
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.keyDown(trigger, { key: "Enter" });
    expect(navigate).toHaveBeenCalledWith(targets[0]);
  });

  it.each(["Escape", "close button"])("returns the current keyboard opener without scrolling after %s", async action => {
    setup();
    const trigger = screen.getByRole("button", { name: "pdfCitation.view [12]" });
    const focus = vi.spyOn(trigger, "focus");
    act(() => trigger.focus());
    await act(async () => {});
    const jump = screen.getByRole("button", { name: "pdfCitation.jump [12]" });
    act(() => jump.focus());
    focus.mockClear();
    if (action === "Escape") fireEvent.keyDown(jump, { key: "Escape" });
    else fireEvent.click(screen.getByRole("button", { name: "pdfCitation.close" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(trigger).toHaveFocus();
    expect(focus).toHaveBeenCalledTimes(1);
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  });

  it.each([
    { dismiss: "scroll", close: "Escape" },
    { dismiss: "close button", close: "close button" },
  ])("does not reuse a keyboard opener after $dismiss and a new pointer preview closed by $close", async ({ dismiss, close }) => {
    const { page } = setup();
    const trigger = screen.getByRole("button", { name: "pdfCitation.view [12]" });
    const focus = vi.spyOn(trigger, "focus");
    act(() => trigger.focus());
    await act(async () => {});
    if (dismiss === "scroll") fireEvent.scroll(page);
    else fireEvent.click(screen.getByRole("button", { name: "pdfCitation.close" }));
    const outside = screen.getByRole("link");
    act(() => outside.focus());
    focus.mockClear();
    await hover(page);
    if (close === "Escape") fireEvent.keyDown(document, { key: "Escape" });
    else fireEvent.click(screen.getByRole("button", { name: "pdfCitation.close" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(outside).toHaveFocus();
    expect(focus).not.toHaveBeenCalled();
  });

  it("restores only the active page keyboard opener when another layer has an old opener", async () => {
    const first = setup();
    render(<div data-testid="page-two"><div className="react-pdf__Page__textContent"><span>[12]</span></div><PdfCitationLayer {...first.props}/></div>);
    const [oldTrigger, currentTrigger] = screen.getAllByRole("button", { name: "pdfCitation.view [12]" });
    act(() => oldTrigger.focus());
    await act(async () => {});
    fireEvent.scroll(first.page);
    act(() => currentTrigger.focus());
    await act(async () => {});
    act(() => screen.getByRole("button", { name: "pdfCitation.jump [12]" }).focus());
    const oldFocus = vi.spyOn(oldTrigger, "focus");
    const currentFocus = vi.spyOn(currentTrigger, "focus");
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(currentTrigger).toHaveFocus();
    expect(currentFocus).toHaveBeenCalledTimes(1);
    expect(currentFocus).toHaveBeenCalledWith({ preventScroll: true });
    expect(oldFocus).not.toHaveBeenCalled();
  });

  it("resolves keyboard focus against current range geometry after layout moves", async () => {
    setup();
    const trigger = screen.getByRole("button", { name: "pdfCitation.view [12]" });
    Object.defineProperty(Range.prototype, "getClientRects", { configurable: true, value() { return [{ left: 100, right: 160, top: 100, bottom: 120, width: 60, height: 20 }]; } });
    fireEvent.focus(trigger);
    await act(async () => {});
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("keeps keyboard hit boxes page-local across scrolling and ordinary rerenders", async () => {
    let pageTop = 100;
    let markerTop = 300;
    const elementRect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      if ((this as HTMLElement).getAttribute("data-testid") === "page") {
        return { left: 0, right: 800, top: pageTop, bottom: pageTop + 1000, width: 800, height: 1000 } as DOMRect;
      }
      return { left: 0, right: 0, top: 0, bottom: 0, width: 0, height: 0 } as DOMRect;
    });
    Object.defineProperty(Range.prototype, "getClientRects", { configurable: true, value() {
      return [{ left: 10, right: 60, top: markerTop, bottom: markerTop + 20, width: 50, height: 20 }];
    } });
    const { page } = setup(new Promise(() => {}));
    const trigger = screen.getByRole("button", { name: "pdfCitation.view [12]" });
    expect(trigger).toHaveStyle({ top: "200px" });

    pageTop = 0;
    markerTop = 200;
    fireEvent.pointerMove(page, { clientX: 20, clientY: 210, buttons: 0 });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(trigger).toHaveStyle({ top: "200px" });
    elementRect.mockRestore();
  });

  it("remeasures page-local keyboard hit boxes when only the page container resizes", async () => {
    let resize!: ResizeObserverCallback;
    vi.stubGlobal("ResizeObserver", class {
      constructor(callback: ResizeObserverCallback) { resize = callback; }
      observe() {}
      disconnect() {}
      unobserve() {}
    });
    let pageLeft = 100;
    let markerLeft = 300;
    let markerWidth = 50;
    const elementRect = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      if ((this as HTMLElement).getAttribute("data-testid") === "page") {
        return { left: pageLeft, right: pageLeft + 800, top: 0, bottom: 1000, width: 800, height: 1000 } as DOMRect;
      }
      return { left: 0, right: 0, top: 0, bottom: 0, width: 0, height: 0 } as DOMRect;
    });
    Object.defineProperty(Range.prototype, "getClientRects", { configurable: true, value() {
      return [{ left: markerLeft, right: markerLeft + markerWidth, top: 20, bottom: 40, width: markerWidth, height: 20 }];
    } });
    setup();
    const trigger = screen.getByRole("button", { name: "pdfCitation.view [12]" });
    expect(trigger).toHaveStyle({ left: "200px", width: "50px" });

    pageLeft = 50;
    markerLeft = 200;
    markerWidth = 40;
    expect(resize).toBeTypeOf("function");
    act(() => resize([], {} as ResizeObserver));
    expect(trigger).toHaveStyle({ left: "150px", width: "40px" });
    elementRect.mockRestore();
  });

  it("does not restore an old trigger when Escape is pressed outside a closed preview", async () => {
    const { page } = setup();
    const trigger = screen.getByRole("button", { name: "pdfCitation.view [12]" });
    act(() => trigger.focus());
    await act(async () => {});
    fireEvent.scroll(page);
    const outside = screen.getByRole("link");
    act(() => outside.focus());
    fireEvent.keyDown(outside, { key: "Escape" });
    expect(outside).toHaveFocus();
  });

  it("previews a citation beneath a native annotation anchor while preserving native activation", async () => {
    const { navigate } = setup();
    const link = screen.getByRole("link");
    fireEvent.pointerMove(link, { clientX: 20, clientY: 20 });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    fireEvent.pointerDown(link, { clientX: 20, clientY: 20 });
    fireEvent.click(link, { clientX: 20, clientY: 20 });
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.keyDown(document, { key: "Escape" });

    fireEvent.pointerMove(link, { clientX: 80, clientY: 20 });
    await act(async () => { vi.advanceTimersByTime(350); });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("previews a known native citation when PDF text rectangles are out of visual order", async () => {
    Object.defineProperty(Range.prototype, "getClientRects", { configurable: true, value() {
      return [
        { left: 30, right: 40, top: 10, bottom: 30, width: 10, height: 20 },
        { left: 10, right: 20, top: 10, bottom: 30, width: 10, height: 20 },
        { left: 20, right: 30, top: 10, bottom: 30, width: 10, height: 20 },
      ];
    } });
    const { page, navigate } = setup();
    const layer = document.createElement("div");
    layer.className = "annotationLayer";
    layer.innerHTML = '<section class="linkAnnotation" style="border-color: rgb(255, 0, 0)"><a title="12" href="#section-12"></a></section><section class="linkAnnotation" style="border-color: rgb(0, 255, 0)"><a title="12" href="#reference-12"></a></section>';
    page.append(layer);
    expect(screen.queryByRole("button", { name: "pdfCitation.view [12]" })).toBeNull();

    const [sectionAnchor, anchor] = layer.querySelectorAll("a");
    fireEvent.pointerMove(sectionAnchor, { clientX: 20, clientY: 20 });
    await act(async () => { vi.advanceTimersByTime(350); });
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.pointerMove(anchor, { clientX: 20, clientY: 20 });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(screen.getByRole("dialog", { name: "pdfCitation.title [12]" })).toBeInTheDocument();
    fireEvent.click(anchor);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("preserves modifiers, dragging and text selection", async () => {
    const { page, navigate } = setup();
    const link = screen.getByRole("link");
    for (const element of [link, page]) {
      fireEvent.pointerDown(element, { clientX: 20, clientY: 20 });
      fireEvent.click(element, { clientX: 20, clientY: 20, ctrlKey: element === page });
    }
    fireEvent.pointerDown(page, { clientX: 10, clientY: 20 });
    fireEvent.click(page, { clientX: 20, clientY: 20 });
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.pointerDown(page, { clientX: 20, clientY: 20 });
    fireEvent.click(page, { clientX: 20, clientY: 20 });
    expect(navigate).toHaveBeenCalledWith(targets[0]);
  });
});
