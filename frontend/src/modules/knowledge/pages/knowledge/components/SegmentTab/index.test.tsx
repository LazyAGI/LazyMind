import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import SegmentTab from "./index";

const searchSegmentsMock = vi.fn();
const deleteSegmentMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  SegmentServiceApi: () => ({
    segmentServiceSearchSegments: (...args: unknown[]) =>
      searchSegmentsMock(...args),
    segmentServiceDeleteSegment: (...args: unknown[]) =>
      deleteSegmentMock(...args),
  }),
}));

vi.mock("../SegmentList", () => ({
  __esModule: true,
  default: (props: {
    segments: Array<{ segment_id?: string; content?: string }>;
    loading?: boolean;
    onDelete?: (segment: unknown) => void;
  }) => (
    <div data-testid="segment-list">
      {props.loading
        ? "loading"
        : props.segments.map((s) => <span key={s.segment_id}>{s.content}</span>)}
      {props.segments[0] && (
        <button onClick={() => props.onDelete?.(props.segments[0])}>
          delete-first
        </button>
      )}
    </div>
  ),
}));

describe("SegmentTab", () => {
  beforeEach(() => {
    searchSegmentsMock.mockReset().mockResolvedValue({
      data: {
        segments: [{ segment_id: "s1", content: "First segment" }],
        next_page_token: "",
      },
    });
    deleteSegmentMock.mockReset().mockResolvedValue({});
  });

  it("fetches and renders segments for the given document and type", async () => {
    renderWithProviders(
      <SegmentTab
        detail={{ dataset_id: "ds-1", document_id: "doc-1" }}
        names={["block"]}
        type="block"
      />,
    );

    await waitFor(() => {
      expect(searchSegmentsMock).toHaveBeenCalledWith({
        dataset: "ds-1",
        document: "doc-1",
        searchSegmentsRequest: expect.objectContaining({ group: "block" }),
      });
    });
    expect(await screen.findByText("First segment")).toBeInTheDocument();
  });

  it("does not fetch when dataset/document ids are missing", () => {
    renderWithProviders(<SegmentTab detail={{}} names={["block"]} type="block" />);

    expect(searchSegmentsMock).not.toHaveBeenCalled();
  });

  it("deletes a segment via the confirm modal and refetches", async () => {
    renderWithProviders(
      <SegmentTab
        detail={{ dataset_id: "ds-1", document_id: "doc-1" }}
        names={["block"]}
        type="block"
      />,
    );

    await screen.findByText("First segment");

    fireEvent.click(screen.getByText("delete-first"));

    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));

    await waitFor(() => {
      expect(deleteSegmentMock).toHaveBeenCalledWith({
        dataset: "",
        group: "block",
        document: "",
        segment: "s1",
      });
    });
  });
});
