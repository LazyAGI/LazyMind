import { createRef } from "react";
import { describe, it, expect, vi } from "vitest";
import { fireEvent, screen, renderWithProviders } from "@/test/testUtils";
import SegmentList, { SegmentListImperativeProps } from "./index";

// react-virtuoso relies on real layout measurement (ResizeObserver/scroll
// height) to decide which rows to render, which jsdom cannot provide. Stub it
// with a simple implementation that renders every item so behavior can be tested.
vi.mock("react-virtuoso", () => ({
  Virtuoso: (props: {
    totalCount: number;
    itemContent: (index: number) => React.ReactNode;
  }) => (
    <div data-testid="virtuoso-stub">
      {Array.from({ length: props.totalCount }, (_, index) => (
        <div key={index}>{props.itemContent(index)}</div>
      ))}
    </div>
  ),
}));

vi.mock("../SegmentCard", () => ({
  __esModule: true,
  default: (props: {
    segment: { segment_id?: string; content?: string };
    onDelete: () => void;
    onOpenDetail: () => void;
  }) => (
    <div data-testid={`segment-card-${props.segment.segment_id}`}>
      {props.segment.content}
      <button onClick={props.onDelete}>delete</button>
      <button onClick={props.onOpenDetail}>open</button>
    </div>
  ),
}));

vi.mock("../SegmentDetailModal", () => ({
  __esModule: true,
  default: () => <div data-testid="segment-detail-modal" />,
}));

describe("SegmentList", () => {
  const segments = [
    { segment_id: "s1", content: "First" },
    { segment_id: "s2", content: "Second" },
  ];

  it("shows a loading state when loading is true", () => {
    renderWithProviders(
      <SegmentList
        segments={[]}
        group="block"
        editable={false}
        hasMoreSegment={false}
        onRefresh={vi.fn()}
        fetchSegments={vi.fn()}
        contentReadOnly
        loading
      />,
    );

    expect(screen.getByText("common.loading")).toBeInTheDocument();
  });

  it("shows an empty state when there are no segments", () => {
    renderWithProviders(
      <SegmentList
        segments={[]}
        group="block"
        editable={false}
        hasMoreSegment={false}
        onRefresh={vi.fn()}
        fetchSegments={vi.fn()}
        contentReadOnly
      />,
    );

    expect(screen.getByText("knowledge.noContent")).toBeInTheDocument();
  });

  it("renders segment cards for each segment", () => {
    renderWithProviders(
      <SegmentList
        segments={segments as any}
        group="block"
        editable={false}
        hasMoreSegment={false}
        onRefresh={vi.fn()}
        fetchSegments={vi.fn()}
        contentReadOnly
      />,
    );

    expect(screen.getByTestId("segment-card-s1")).toHaveTextContent("First");
    expect(screen.getByTestId("segment-card-s2")).toHaveTextContent("Second");
  });

  it("calls onDelete with the correct segment when a card's delete is clicked", () => {
    const onDelete = vi.fn();
    renderWithProviders(
      <SegmentList
        segments={segments as any}
        group="block"
        editable={false}
        hasMoreSegment={false}
        onDelete={onDelete}
        onRefresh={vi.fn()}
        fetchSegments={vi.fn()}
        contentReadOnly
      />,
    );

    fireEvent.click(screen.getByTestId("segment-card-s1").querySelector("button")!);
    expect(onDelete).toHaveBeenCalledWith(segments[0]);
  });

  it("opens the detail modal through onGetItemInfo when provided, without needing contentReadOnly", () => {
    const onGetItemInfo = vi.fn();
    const ref = createRef<SegmentListImperativeProps>();
    renderWithProviders(
      <SegmentList
        ref={ref}
        segments={segments as any}
        group="block"
        editable={false}
        hasMoreSegment={false}
        onRefresh={vi.fn()}
        fetchSegments={vi.fn()}
        contentReadOnly={false}
        onGetItemInfo={onGetItemInfo}
      />,
    );

    const openButtons = screen.getAllByText("open");
    fireEvent.click(openButtons[0]);

    expect(onGetItemInfo).toHaveBeenCalledWith(segments[0]);
  });

  it("exposes openDetail via ref which delegates to onGetItemInfo", () => {
    const onGetItemInfo = vi.fn();
    const ref = createRef<SegmentListImperativeProps>();
    renderWithProviders(
      <SegmentList
        ref={ref}
        segments={segments as any}
        group="block"
        editable={false}
        hasMoreSegment={false}
        onRefresh={vi.fn()}
        fetchSegments={vi.fn()}
        contentReadOnly={false}
        onGetItemInfo={onGetItemInfo}
      />,
    );

    ref.current?.openDetail(segments[0] as any, "block");

    expect(onGetItemInfo).toHaveBeenCalledWith(segments[0]);
  });
});
