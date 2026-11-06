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
function setup(preview: Promise<PdfCitationPreview | undefined> | (() => Promise<PdfCitationPreview | undefined>) = Promise.resolve(undefined), rawText?: string, onAddToLibrary?: (target: typeof targets[number]) => Promise<void>) {
  const navigate = vi.fn();
  const cache = { get: vi.fn(() => typeof preview === "function" ? preview() : preview), dispose: vi.fn(), disposeDocument: vi.fn() } satisfies ReturnType<typeof createPdfCitationPreviewCache>;
  const props = { targets: rawText === undefined ? targets : groupPdfCitationTargets([{ referenceId: "r12", referenceKey: "12", rawText, kind: "download", page: 8, bbox: [20, 60, 300, 84] }]), getDocument: () => pdf, previewCache: cache, onNavigate: navigate, onAddToLibrary };
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

function setupAuthorYearNativeLink(color: "green" | "red", destinationTop = 495.393) {
  const source = { referenceId: "agarwal-2019", referenceKey: "", rawText: "Agarwal, A. Reinforcement learning: Theory and algorithms. 2019.", kind: "download" as const, page: 9, bbox: [307.44, 300.93, 543.18, 334.88] as [number, number, number, number] };
  const authorYearTargets = groupPdfCitationTargets([source]);
  const annotation = {
    id: "208R",
    subtype: "Link",
    rect: [10, 762, 60, 782],
    borderColor: new Uint8ClampedArray(color === "green" ? [0, 255, 0] : [255, 0, 0]),
    dest: "cite.agarwal2019reinforcement",
  };
  const viewport = {
    width: 612,
    height: 792,
    convertToViewportPoint: (x: number, y: number) => [x, 792 - y],
    convertToViewportRectangle: ([x0, y0, x1, y1]: number[]) => [x0, 792 - y0, x1, 792 - y1],
  };
  const pdf = {
    getPage: vi.fn(async (pageNumber: number) => ({
      rotate: 0,
      getAnnotations: async () => pageNumber === 1 ? [annotation] : [],
      getViewport: () => viewport,
    })),
    getDestination: vi.fn(async () => [{ num: 705, gen: 0 }, { name: "XYZ" }, 287.515, destinationTop, null]),
    getPageIndex: vi.fn(async () => 8),
  } as unknown as PdfCitationPreviewDocument;
  const cache = { get: vi.fn(async () => undefined), dispose: vi.fn(), disposeDocument: vi.fn() } satisfies ReturnType<typeof createPdfCitationPreviewCache>;
  const navigate = vi.fn();
  const view = render(<div className="react-pdf__Page" data-pdf-page-index="0" data-page-number="1" data-testid="author-year-page">
    <div className="react-pdf__Page__textContent"><span>See Agarwal et al. (2019) for details.</span></div>
    <div className="annotationLayer"><section className="linkAnnotation" data-annotation-id="208R" style={{ borderColor: color === "green" ? "rgb(0, 255, 0)" : "rgb(255, 0, 0)" }}><a data-element-id="208R" title="Agarwal et al. (2019)" href="#" /></section></div>
    <PdfCitationLayer targets={authorYearTargets} getDocument={() => pdf} previewCache={cache} onNavigate={navigate} />
  </div>);
  const page = screen.getByTestId("author-year-page");
  const link = page.querySelector<HTMLAnchorElement>(".linkAnnotation a")!;
  vi.spyOn(page, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, right: 612, bottom: 792, width: 612, height: 792 } as DOMRect);
  vi.spyOn(link, "getBoundingClientRect").mockReturnValue({ left: 10, top: 10, right: 60, bottom: 30, width: 50, height: 20 } as DOMRect);
  return { ...view, page, link, navigate, cache, source, pdf };
}

function setupNumericNativeLink(getPageIndex: () => Promise<number> = async () => 22) {
  const numericTargets = groupPdfCitationTargets([{
    referenceId: "shin2021rtic", referenceKey: "45",
    rawText: "Minchul Shin, Yoonjae Cho, Byungsoo Ko, and Geonmo Gu. Rtic: Residual Learning for Text and Image Composition using Graph Convolutional Network. 2021.",
    kind: "download", page: 0,
  }]);
  const pdf = {
    numPages: 30,
    getPage: vi.fn(async () => ({ getAnnotations: async () => [{ id: "197R", dest: "cite.shin2021rtic" }] })),
    getDestination: vi.fn(async () => [{ num: 880, gen: 0 }, { name: "XYZ" }, 80.697, 428.364, null]),
    getPageIndex: vi.fn(getPageIndex),
  } as unknown as PdfCitationPreviewDocument;
  const cache = { get: vi.fn(async () => undefined), dispose: vi.fn(), disposeDocument: vi.fn() } satisfies ReturnType<typeof createPdfCitationPreviewCache>;
  const navigate = vi.fn();
  const view = render(<div data-pdf-page-index="0" data-testid="numeric-page">
    <div className="annotationLayer"><section className="linkAnnotation" style={{ borderColor: "rgb(0, 255, 0)" }}>
      <a data-element-id="197R" title="45" href="#reference-45" />
    </section></div>
    <PdfCitationLayer targets={numericTargets} getDocument={() => pdf} previewCache={cache} onNavigate={navigate} />
  </div>);
  return { ...view, pdf, cache, navigate, target: numericTargets[0], page: screen.getByTestId("numeric-page"), link: view.container.querySelector<HTMLAnchorElement>(".linkAnnotation a")! };
}

describe("citation preview interaction", () => {
  it("uses a numeric native link destination to locate a reference with no stored region", async () => {
    const { pdf, cache, navigate, target, link } = setupNumericNativeLink();
    fireEvent.pointerMove(link, { clientX: 20, clientY: 20, buttons: 0 });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(screen.getByRole("dialog")).toHaveTextContent(target.rawText);
    expect(cache.get).toHaveBeenCalledWith(pdf, { ...target, fallbackPage: 23 });
    fireEvent.click(screen.getByRole("button", { name: "pdfCitation.jump [45]" }));
    expect(navigate).toHaveBeenCalledWith({ ...target, fallbackPage: 23 });
    fireEvent.click(link, { clientX: 20, clientY: 20 });
    expect(navigate).toHaveBeenCalledTimes(1);
  });

  it("does not start a numeric preview after the pointer leaves during destination lookup", async () => {
    let finish!: (pageIndex: number) => void;
    const { cache, link, page } = setupNumericNativeLink(() => new Promise(resolve => { finish = resolve; }));
    fireEvent.pointerMove(link, { clientX: 20, clientY: 20, buttons: 0 });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(cache.get).not.toHaveBeenCalled();
    fireEvent.pointerLeave(page);
    await act(async () => { finish(22); });
    expect(cache.get).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("previews an author-year citation from its native green link destination and keeps native clicking", async () => {
    const { link, navigate, cache, source } = setupAuthorYearNativeLink("green");
    await act(async () => {});
    fireEvent.pointerMove(link, { clientX: 20, clientY: 20, buttons: 0 });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(screen.getByRole("dialog").querySelector(".pdf-citation-raw")?.textContent).toBe(source.rawText);
    expect(cache.get).toHaveBeenCalled();
    fireEvent.click(link, { clientX: 20, clientY: 20 });
    expect(navigate).not.toHaveBeenCalled();
  });

  it.each([
    { color: "red" as const, destinationTop: 495.393 },
    { color: "green" as const, destinationTop: 100 },
  ])("does not preview a $color native link with destination top $destinationTop", async ({ color, destinationTop }) => {
    const { link, cache } = setupAuthorYearNativeLink(color, destinationTop);
    await act(async () => {});
    fireEvent.pointerMove(link, { clientX: 20, clientY: 20, buttons: 0 });
    await act(async () => { vi.advanceTimersByTime(350); });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(cache.get).not.toHaveBeenCalled();
  });

  it("shows the library action only when the current collection can accept papers", async () => {
    const { page, unmount } = setup();
    await hover(page);
    expect(screen.queryByRole("button", { name: "pdfCitation.addToLibrary" })).toBeNull();
    unmount();
    const add = vi.fn().mockResolvedValue(undefined);
    const other = setup(Promise.resolve(undefined), undefined, add);
    await hover(other.page);
    expect(screen.getByRole("button", { name: "pdfCitation.addToLibrary" })).toBeInTheDocument();
  });

  it("adds a reference once without navigating and keeps the success state in the card", async () => {
    let complete!: () => void;
    const add = vi.fn(() => new Promise<void>(resolve => { complete = resolve; }));
    const { page, navigate } = setup(Promise.resolve(undefined), undefined, add);
    await hover(page);
    const button = screen.getByRole("button", { name: "pdfCitation.addToLibrary" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(add).toHaveBeenCalledTimes(1);
    expect(add).toHaveBeenCalledWith(targets[0]);
    expect(navigate).not.toHaveBeenCalled();
    expect(button).toBeDisabled();
    await act(async () => complete());
    expect(screen.getByRole("button", { name: "pdfCitation.addedToLibrary" })).toBeDisabled();
  });

  it("allows retry if adding a reference fails", async () => {
    const add = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(undefined);
    const { page } = setup(Promise.resolve(undefined), undefined, add);
    await hover(page);
    const button = screen.getByRole("button", { name: "pdfCitation.addToLibrary" });
    fireEvent.click(button);
    await act(async () => {});
    expect(button).not.toBeDisabled();
    fireEvent.click(button);
    await act(async () => {});
    expect(add).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("button", { name: "pdfCitation.addedToLibrary" })).toBeDisabled();
  });
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

  it("uses the located crop region to navigate an old reference with no stored bbox", async () => {
    const region = { page: 23, bbox: [80, 360, 532, 386] as [number, number, number, number] };
    const noBbox = groupPdfCitationTargets([{ referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 0 }]);
    let resolve!: (value: PdfCitationPreview) => void;
    const cache = { get: vi.fn(() => new Promise<PdfCitationPreview>(done => { resolve = done; })), dispose: vi.fn(), disposeDocument: vi.fn() } satisfies ReturnType<typeof createPdfCitationPreviewCache>;
    const navigate = vi.fn();
    const view = render(<div data-testid="page"><div className="react-pdf__Page__textContent"><span>[12]</span></div>
      <PdfCitationLayer targets={noBbox} getDocument={() => pdf} previewCache={cache} onNavigate={navigate}/></div>);
    await hover(screen.getByTestId("page"));
    expect(screen.getByRole("button", { name: "pdfCitation.jump [12]" })).toBeDisabled();
    await act(async () => resolve({ url: "blob:found", width: 300, height: 60, region }));
    expect(screen.getByRole("img")).toHaveAttribute("src", "blob:found");
    expect(screen.getByRole("button", { name: "pdfCitation.jump [12]" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "pdfCitation.jump [12]" }));
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ referenceId: "r12", fallbackPage: 23, regions: [region] }));
    view.unmount();
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
