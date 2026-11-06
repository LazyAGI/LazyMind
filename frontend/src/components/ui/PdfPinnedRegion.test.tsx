import { fireEvent, render, screen } from "@testing-library/react";
import { useLayoutEffect, useRef, useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cropPdfBlockPreview } from "./pdfBlockPreview";
import { PdfPinnedRegion } from "./PdfPinnedRegion";
import type { PdfRegionPinRecord } from "./pdfPinStore";

vi.mock("./pdfBlockPreview", () => ({ cropPdfBlockPreview: vi.fn(() => "data:image/png;base64,fixture") }));

function bounds(left: number, top: number, width: number, height: number): DOMRect {
  return { left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON: () => ({}) } as DOMRect;
}

let viewportWidth = 600;

function Reader({ source = "paper-a", onMinimize, pins, onAddPin, onUpdatePin, onRemovePin, startCapture = true }: { source?: string; onMinimize?: (id:string)=>void; pins?: PdfRegionPinRecord[]; onAddPin?: (pin:PdfRegionPinRecord)=>boolean; onUpdatePin?: (pin:PdfRegionPinRecord)=>void; onRemovePin?: (id:string)=>void; startCapture?: boolean }) {
  const stageRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const pageRef = useRef<HTMLDivElement>(null);
  const pageRefs = useRef<Array<HTMLDivElement | null>>([]);
  const [active, setActive] = useState(startCapture);
  useLayoutEffect(() => { pageRefs.current = [pageRef.current]; }, []);
  return <div ref={stageRef} data-testid="stage">
    <div ref={viewportRef} data-testid="viewport"><div ref={pageRef} data-testid="page" /></div>
    <button type="button" onClick={() => setActive(true)}>Start capture</button>
    <PdfPinnedRegion stageRef={stageRef} viewportRef={viewportRef} pageRefs={pageRefs} sourceKey={source} active={active} onActiveChange={setActive} onMinimizePin={onMinimize} pins={pins} onAddPin={onAddPin} onUpdatePin={onUpdatePin} onRemovePin={onRemovePin} language="en" />
  </div>;
}

beforeEach(() => {
  viewportWidth = 600;
  vi.mocked(cropPdfBlockPreview).mockReset().mockReturnValue("data:image/png;base64,fixture");
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    if (this.dataset.testid === "stage") return bounds(0, 0, 800, 600);
    if (this.dataset.testid === "viewport") return bounds(0, 40, viewportWidth, 560);
    if (this.dataset.testid === "page") return bounds(80, 60, 400, 520);
    return bounds(0, 0, 0, 0);
  });
});
afterEach(() => vi.restoreAllMocks());

function selectRegion(endX = 300, endY = 300) {
  const selector = screen.getByRole("dialog", { name: "Capture and pin region" });
  fireEvent.pointerDown(selector, { button: 0, pointerId: 1, clientX: 100, clientY: 100 });
  fireEvent.pointerMove(selector, { pointerId: 1, clientX: endX, clientY: endY });
  fireEvent.pointerUp(selector, { pointerId: 1 });
  return selector;
}

it.each([
  [400, 180, 300 / 80],
  [180, 400, 80 / 300],
])("opens the pinned frame at the selected aspect ratio (%i, %i)", (endX, endY, aspect) => {
  render(<Reader />);
  selectRegion(endX, endY);
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  const panel = screen.getByRole("region", { name: "Pinned PDF region" });
  const width = Number.parseFloat(panel.style.width);
  const height = Number.parseFloat(panel.style.height);
  expect(width / height).toBeCloseTo(aspect, 2);
  expect(width).toBeLessThanOrEqual(480);
  expect(height).toBeLessThanOrEqual(544);
});

it("moves the pinned frame from its image or empty surface, while close and resize remain separate", () => {
  render(<Reader />);
  selectRegion();
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  const panel = screen.getByRole("region", { name: "Pinned PDF region" });
  const image = screen.getByRole("img", { name: "Captured region from page 1" });
  const initialLeft = Number.parseFloat(panel.style.left);
  fireEvent.pointerDown(image, { button: 0, pointerId: 7, clientX: 450, clientY: 150 });
  fireEvent.pointerMove(panel, { pointerId: 7, clientX: 400, clientY: 170 });
  fireEvent.pointerUp(panel, { pointerId: 7 });
  expect(Number.parseFloat(panel.style.left)).toBeCloseTo(initialLeft - 50);

  const imageDraggedLeft = Number.parseFloat(panel.style.left);
  fireEvent.pointerDown(panel, { button: 0, pointerId: 8, clientX: 430, clientY: 160 });
  fireEvent.pointerMove(panel, { pointerId: 8, clientX: 410, clientY: 160 });
  fireEvent.pointerUp(panel, { pointerId: 8 });
  expect(Number.parseFloat(panel.style.left)).toBeCloseTo(imageDraggedLeft - 20);

  const beforeResize = Number.parseFloat(panel.style.left);
  const east = panel.querySelector('[data-pin-resize="e"]')!;
  fireEvent.pointerDown(east, { button: 0, pointerId: 9, clientX: 500, clientY: 170 });
  fireEvent.pointerMove(east, { pointerId: 9, clientX: 520, clientY: 170 });
  fireEvent.pointerUp(east, { pointerId: 9 });
  expect(Number.parseFloat(panel.style.left)).toBeCloseTo(beforeResize);
  fireEvent.click(screen.getByRole("button", { name: "Close pinned region" }));
  expect(screen.queryByRole("region", { name: "Pinned PDF region" })).toBeNull();
});

it("minimizes a crop without deleting it, then restores the same image and geometry", () => {
  const onMinimize = vi.fn();
  const view = render(<Reader onMinimize={onMinimize} />);
  selectRegion();
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  const panel = screen.getByRole("region", { name: "Pinned PDF region" });
  const originalGeometry = {left:panel.style.left,top:panel.style.top,width:panel.style.width,height:panel.style.height};
  fireEvent.click(screen.getByRole("button", { name: "Minimize pinned region" }));
  expect(screen.queryByRole("region", { name: "Pinned PDF region" })).toBeNull();
  expect(onMinimize).toHaveBeenCalledWith(expect.any(String));
  fireEvent.click(screen.getByRole("button", { name: "Restore pinned region" }));
  expect(screen.getByRole("region", { name: "Pinned PDF region" })).toHaveStyle(originalGeometry);
  expect(screen.getByRole("img", { name: "Captured region from page 1" })).toHaveAttribute("src", "data:image/png;base64,fixture");
  view.unmount();
});

it("keeps the selection editable when the controlled pin board is full", () => {
  const onAddPin = vi.fn(() => false);
  render(<Reader pins={[]} onAddPin={onAddPin} />);
  selectRegion();
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  expect(onAddPin).toHaveBeenCalledWith(expect.objectContaining({kind:"region",page:1,bbox:expect.any(Array),image:"data:image/png;base64,fixture"}));
  expect(screen.getByRole("dialog", { name: "Capture and pin region" })).toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("Six items are pinned");
});

it("commits controlled region geometry after a drag and a resize", () => {
  const original:PdfRegionPinRecord={id:"saved-crop",kind:"region",title:"Crop · Page 1",geometry:{x:300,y:60,width:200,height:160},minimized:false,page:1,bbox:[.1,.1,.5,.4],image:"data:image/png;base64,fixture"};
  const onUpdatePin=vi.fn();
  render(<Reader pins={[original]} startCapture={false} onUpdatePin={onUpdatePin} />);
  const panel=screen.getByRole("region",{name:"Pinned PDF region"});
  fireEvent.pointerDown(panel,{button:0,pointerId:7,clientX:350,clientY:150});
  fireEvent.pointerMove(panel,{pointerId:7,clientX:320,clientY:180});
  fireEvent.pointerUp(panel,{pointerId:7});
  expect(onUpdatePin).toHaveBeenCalledWith(expect.objectContaining({id:"saved-crop",geometry:{x:270,y:90,width:200,height:160}}));
  const east=panel.querySelector('[data-pin-resize="e"]')!;
  fireEvent.pointerDown(east,{button:0,pointerId:8,clientX:500,clientY:170});
  fireEvent.pointerMove(east,{pointerId:8,clientX:530,clientY:170});
  fireEvent.pointerUp(east,{pointerId:8});
  expect(onUpdatePin).toHaveBeenCalledWith(expect.objectContaining({id:"saved-crop",geometry:expect.objectContaining({width:230})}));
});

it("renders several controlled crops and minimizes the selected one", () => {
  const first:PdfRegionPinRecord={id:"first",kind:"region",title:"First",geometry:{x:220,y:60,width:200,height:160},minimized:false,page:1,bbox:[.1,.1,.5,.4],image:"data:image/png;base64,first"};
  const second:PdfRegionPinRecord={...first,id:"second",title:"Second",page:2,geometry:{x:260,y:100,width:200,height:160},image:"data:image/png;base64,second"};
  const onMinimize=vi.fn();
  const view=render(<Reader pins={[first,second]} startCapture={false} onMinimize={onMinimize} />);
  expect(screen.getAllByRole("region",{name:"Pinned PDF region"})).toHaveLength(2);
  fireEvent.click(screen.getAllByRole("button",{name:"Minimize pinned region"})[1]);
  expect(onMinimize).toHaveBeenCalledWith("second");
  view.rerender(<Reader pins={[first,{...second,minimized:true}]} startCapture={false} onMinimize={onMinimize} />);
  expect(screen.getAllByRole("region",{name:"Pinned PDF region"})).toHaveLength(1);
  expect(screen.getByRole("img",{name:"Captured region from page 1"})).toBeInTheDocument();
});

it("offers keyboard movement and resizing when the pinned frame has focus", () => {
  render(<Reader />);
  selectRegion();
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  const panel = screen.getByRole("region", { name: "Pinned PDF region" });
  expect(panel).toHaveAttribute("tabindex", "0");
  const startX = Number.parseFloat(panel.style.left);
  const startWidth = Number.parseFloat(panel.style.width);
  fireEvent.keyDown(panel, { key: "ArrowLeft" });
  expect(Number.parseFloat(panel.style.left)).toBeLessThan(startX);
  fireEvent.keyDown(panel, { key: "ArrowRight", shiftKey: true });
  expect(Number.parseFloat(panel.style.width)).toBeGreaterThan(startWidth);
});

it("shades only the reading viewport, reveals the crop, then pins a real page image", () => {
  render(<Reader />);
  const selector = selectRegion();
  expect(selector).toHaveStyle({ left: "0px", top: "40px", width: "600px", height: "560px" });
  expect(selector.querySelectorAll(".pdf-pin-shade")).toHaveLength(4);
  expect(selector.querySelector(".pdf-pin-selection")).toHaveStyle({ left: "100px", top: "60px", width: "200px", height: "200px" });
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  expect(cropPdfBlockPreview).toHaveBeenCalledOnce();
  const [page, bbox] = vi.mocked(cropPdfBlockPreview).mock.calls[0];
  expect(page).toBe(screen.getByTestId("page"));
  expect(bbox?.[0]).toBeCloseTo(.05);
  expect(bbox?.[1]).toBeCloseTo(40 / 520);
  expect(bbox?.[2]).toBeCloseTo(.55);
  expect(bbox?.[3]).toBeCloseTo(240 / 520);
  expect(screen.queryByRole("dialog", { name: "Capture and pin region" })).toBeNull();
  expect(screen.getByRole("region", { name: "Pinned PDF region" })).toHaveTextContent("Page 1");
  expect(screen.getByRole("img", { name: "Captured region from page 1" })).toHaveAttribute("src", "data:image/png;base64,fixture");
  fireEvent.click(screen.getByRole("button", { name: "Close pinned region" }));
  expect(screen.queryByRole("region", { name: "Pinned PDF region" })).toBeNull();
});

it("resizes a selected region before capture and keeps it on the source page", () => {
  render(<Reader />);
  const selector = selectRegion();
  const southEast = selector.querySelector('[data-pin-handle="se"]')!;
  fireEvent.pointerDown(southEast, { button: 0, pointerId: 2, clientX: 300, clientY: 300 });
  fireEvent.pointerMove(selector, { pointerId: 2, clientX: 360, clientY: 360 });
  fireEvent.pointerUp(selector, { pointerId: 2 });
  expect(selector.querySelector(".pdf-pin-selection")).toHaveStyle({ width: "260px", height: "260px" });
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  const bbox = vi.mocked(cropPdfBlockPreview).mock.calls[0][1];
  expect(bbox?.[2]).toBeCloseTo(.7);
  expect(bbox?.[3]).toBeCloseTo(300 / 520);
});

it("keeps the editable selection when the page canvas is not ready", () => {
  vi.mocked(cropPdfBlockPreview).mockReturnValue(undefined);
  render(<Reader />);
  selectRegion();
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  expect(screen.getByRole("status")).toHaveTextContent("still rendering");
  expect(screen.getByRole("dialog", { name: "Capture and pin region" })).toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "Pinned PDF region" })).toBeNull();
});

it("clears the previous crop when a different PDF is opened", () => {
  const view = render(<Reader />);
  selectRegion();
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  expect(screen.getByRole("region", { name: "Pinned PDF region" })).toBeInTheDocument();
  view.rerender(<Reader source="paper-b" />);
  expect(screen.queryByRole("region", { name: "Pinned PDF region" })).toBeNull();
});

it("keeps dragged and edge-resized geometry after a new capture is cancelled", () => {
  render(<Reader />);
  selectRegion();
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  const panel = screen.getByRole("region", { name: "Pinned PDF region" });
  const header = panel.querySelector(".pdf-pin-floating-header")!;
  fireEvent.pointerDown(header, { button: 0, pointerId: 4, clientX: 450, clientY: 65 });
  fireEvent.pointerMove(header, { pointerId: 4, clientX: 390, clientY: 85 });
  fireEvent.pointerUp(header, { pointerId: 4 });
  const left = Number.parseFloat(panel.style.left);
  expect(left).toBeLessThan(388);
  const rightEdge = panel.querySelector('[data-pin-resize="e"]')!;
  fireEvent.pointerDown(rightEdge, { button: 0, pointerId: 5, clientX: 528, clientY: 170 });
  fireEvent.pointerMove(rightEdge, { pointerId: 5, clientX: 558, clientY: 170 });
  fireEvent.pointerUp(rightEdge, { pointerId: 5 });
  const geometry = { left: panel.style.left, top: panel.style.top, width: panel.style.width, height: panel.style.height };
  expect(Number.parseFloat(geometry.width)).toBeGreaterThan(200);
  fireEvent.click(screen.getByRole("button", { name: "Start capture" }));
  fireEvent.keyDown(screen.getByRole("dialog", { name: "Capture and pin region" }), { key: "Escape" });
  expect(screen.getByRole("region", { name: "Pinned PDF region" })).toHaveStyle(geometry);
  expect(screen.queryByRole("dialog", { name: "Capture and pin region" })).toBeNull();
});

it("clamps the pinned window into a narrower reader viewport", () => {
  render(<Reader />);
  selectRegion();
  fireEvent.click(screen.getByRole("button", { name: "Pin region" }));
  const panel = screen.getByRole("region", { name: "Pinned PDF region" });
  viewportWidth = 280;
  fireEvent(window, new Event("resize"));
  expect(Number.parseFloat(panel.style.left) + Number.parseFloat(panel.style.width)).toBeLessThanOrEqual(272);
  expect(Number.parseFloat(panel.style.left)).toBeGreaterThanOrEqual(8);
});
