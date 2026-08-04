import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, waitFor, screen, renderWithProviders } from "@/test/testUtils";
import { act } from "@testing-library/react";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import QaTab from "./index";

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

// `@/components/ui`'s barrel file re-exports RenderPdf, which pulls in
// pdfjs-dist and crashes in jsdom (no DOMMatrix). This component only needs
// ListPageTable, so stub the barrel with a minimal antd Table-backed implementation.
vi.mock("@/components/ui", async () => {
  const { Table } = await import("antd");
  return { ListPageTable: Table };
});

describe("QaTab", () => {
  beforeEach(() => {
    searchSegmentsMock.mockReset().mockResolvedValue({
      data: {
        segments: [
          { segment_id: "q1", content: "What is X?", answer: "X is Y" },
        ],
        total_size: 1,
      },
    });
    deleteSegmentMock.mockReset().mockResolvedValue({});

    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: ["DATASET_WRITE"],
      } as any);
    });
  });

  it("fetches and renders question/answer rows", async () => {
    renderWithProviders(
      <QaTab
        detail={{ dataset_id: "ds-1", document_id: "doc-1" } as any}
        type="qa"
      />,
    );

    await waitFor(() => {
      expect(searchSegmentsMock).toHaveBeenCalled();
    });
    expect(await screen.findByText("What is X?")).toBeInTheDocument();
    expect(screen.getByText("X is Y")).toBeInTheDocument();
  });

  it("shows a delete action when the user has write permission, and deletes on confirm", async () => {
    renderWithProviders(
      <QaTab
        detail={{ dataset_id: "ds-1", document_id: "doc-1" } as any}
        type="qa"
      />,
    );

    await screen.findByText("What is X?");
    fireEvent.click(screen.getByRole("button", { name: "common.delete" }));

    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }));

    await waitFor(() => {
      expect(deleteSegmentMock).toHaveBeenCalledWith({
        dataset: "ds-1",
        document: "doc-1",
        segment: "q1",
        group: "qa",
      });
    });
  });

  it("hides the delete action when the user lacks write permission", async () => {
    act(() => {
      useDatasetPermissionStore.getState().setCurrentDataset({
        acl: ["DATASET_READ"],
      } as any);
    });

    renderWithProviders(
      <QaTab
        detail={{ dataset_id: "ds-1", document_id: "doc-1" } as any}
        type="qa"
      />,
    );

    await screen.findByText("What is X?");
    expect(
      screen.queryByRole("button", { name: "common.delete" }),
    ).not.toBeInTheDocument();
  });
});
