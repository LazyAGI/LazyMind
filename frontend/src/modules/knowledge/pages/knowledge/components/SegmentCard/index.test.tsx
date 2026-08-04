import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import SegmentCard from "./index";

const modifyStatusMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  SegmentServiceApi: () => ({
    segmentServiceModifyStatus: (...args: unknown[]) => modifyStatusMock(...args),
  }),
}));

vi.mock("@/modules/knowledge/pages/knowledge/components/SegmentContent", () => ({
  __esModule: true,
  default: ({ segment }: { segment: { content?: string } }) => (
    <div data-testid="segment-content">{segment.content}</div>
  ),
}));

describe("SegmentCard", () => {
  const segment = {
    segment_id: "s1",
    number: 3,
    content: "Hello world",
    is_active: true,
  };

  beforeEach(() => {
    modifyStatusMock.mockReset().mockResolvedValue({});
  });

  it("renders the segment number and content", () => {
    renderWithProviders(
      <SegmentCard
        segment={segment as any}
        group="block"
        editable={false}
        onDelete={vi.fn()}
        onOpenDetail={vi.fn()}
        onRefresh={vi.fn()}
        contentReadOnly
      />,
    );

    expect(screen.getByText("#3")).toBeInTheDocument();
    expect(screen.getByTestId("segment-content")).toHaveTextContent("Hello world");
  });

  it("hides the number when showNumber is false", () => {
    renderWithProviders(
      <SegmentCard
        segment={segment as any}
        group="block"
        editable={false}
        onDelete={vi.fn()}
        onOpenDetail={vi.fn()}
        onRefresh={vi.fn()}
        contentReadOnly
        showNumber={false}
      />,
    );

    expect(screen.queryByText("#3")).not.toBeInTheDocument();
  });

  it("calls onOpenDetail when the content is clicked", () => {
    const onOpenDetail = vi.fn();
    renderWithProviders(
      <SegmentCard
        segment={segment as any}
        group="block"
        editable={false}
        onDelete={vi.fn()}
        onOpenDetail={onOpenDetail}
        onRefresh={vi.fn()}
        contentReadOnly
      />,
    );

    fireEvent.click(screen.getByTestId("segment-content"));
    expect(onOpenDetail).toHaveBeenCalled();
  });

  it("does not render the switch/delete controls when not editable", () => {
    renderWithProviders(
      <SegmentCard
        segment={segment as any}
        group="block"
        editable={false}
        onDelete={vi.fn()}
        onOpenDetail={vi.fn()}
        onRefresh={vi.fn()}
        contentReadOnly
      />,
    );

    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("calls onDelete when the delete icon is clicked", () => {
    const onDelete = vi.fn();
    const { container } = renderWithProviders(
      <SegmentCard
        segment={segment as any}
        group="block"
        editable
        onDelete={onDelete}
        onOpenDetail={vi.fn()}
        onRefresh={vi.fn()}
        contentReadOnly={false}
      />,
    );

    const deleteIcon = container.querySelector(".delete-icon");
    expect(deleteIcon).toBeTruthy();
    fireEvent.click(deleteIcon!);
    expect(onDelete).toHaveBeenCalled();
  });

  it("uses onUpdateStatus (optimistic path) instead of calling onRefresh directly when toggling", async () => {
    const onUpdateStatus = vi.fn();
    const onRefresh = vi.fn();
    renderWithProviders(
      <SegmentCard
        segment={segment as any}
        group="block"
        editable
        onDelete={vi.fn()}
        onOpenDetail={vi.fn()}
        onRefresh={onRefresh}
        onUpdateStatus={onUpdateStatus}
        contentReadOnly={false}
      />,
    );

    fireEvent.click(screen.getByRole("switch"));

    await waitFor(() => {
      expect(modifyStatusMock).toHaveBeenCalledWith({
        dataset: "",
        document: "",
        segment: "s1",
        modifyStatusRequest: { is_active: false, name: "", group: "block" },
      });
    });
    expect(onUpdateStatus).toHaveBeenCalledWith("s1", false, expect.anything());
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it("falls back to calling onRefresh after the API resolves when onUpdateStatus is not provided", async () => {
    const onRefresh = vi.fn();
    renderWithProviders(
      <SegmentCard
        segment={segment as any}
        group="block"
        editable
        onDelete={vi.fn()}
        onOpenDetail={vi.fn()}
        onRefresh={onRefresh}
        contentReadOnly={false}
      />,
    );

    fireEvent.click(screen.getByRole("switch"));

    await waitFor(() => {
      expect(onRefresh).toHaveBeenCalled();
    });
  });
});
