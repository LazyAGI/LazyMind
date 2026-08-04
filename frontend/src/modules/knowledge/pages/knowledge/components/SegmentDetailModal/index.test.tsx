import { createRef } from "react";
import { describe, it, expect, vi } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import SegmentDetailModal, { ISegmentDetailModalRef } from "./index";

vi.mock("@/modules/knowledge/pages/knowledge/components/SegmentContent", () => ({
  __esModule: true,
  default: ({ segment }: { segment: { content?: string } }) => (
    <div data-testid="segment-content">{segment.content}</div>
  ),
}));

describe("SegmentDetailModal", () => {
  it("is closed until handleOpen is called via ref", () => {
    const ref = createRef<ISegmentDetailModalRef>();
    renderWithProviders(
      <SegmentDetailModal ref={ref} onClose={vi.fn()} editable={false} />,
    );

    expect(screen.queryByText("knowledge.segmentDetail")).not.toBeInTheDocument();
  });

  it("opens with a loading state then shows the segment content", async () => {
    const ref = createRef<ISegmentDetailModalRef>();
    renderWithProviders(
      <SegmentDetailModal ref={ref} onClose={vi.fn()} editable={false} />,
    );

    ref.current?.handleOpen(
      { segment_id: "s1", number: 4, content: "Segment body" } as any,
      "block",
    );

    await waitFor(() => {
      expect(screen.getByTestId("segment-content")).toHaveTextContent(
        "Segment body",
      );
    });
    expect(screen.getByText("#4")).toBeInTheDocument();
    expect(screen.getByText("knowledge.segmentDetail")).toBeInTheDocument();
  });

  it("shows the editable title when editable is true", async () => {
    const ref = createRef<ISegmentDetailModalRef>();
    renderWithProviders(
      <SegmentDetailModal ref={ref} onClose={vi.fn()} editable />,
    );

    ref.current?.handleOpen({ segment_id: "s1", number: 1 } as any, "block");

    await waitFor(() => {
      expect(
        screen.getByText("knowledge.segmentDetailEditable"),
      ).toBeInTheDocument();
    });
  });

  it("calls onClose only when editable and the modal is dismissed", async () => {
    const onClose = vi.fn();
    const ref = createRef<ISegmentDetailModalRef>();
    renderWithProviders(
      <SegmentDetailModal ref={ref} onClose={onClose} editable />,
    );

    ref.current?.handleOpen({ segment_id: "s1", number: 1 } as any, "block");
    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "Close" }));

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled();
    });
  });
});
