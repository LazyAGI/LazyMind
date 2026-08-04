import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import SummaryTab from "./index";

vi.mock("../SegmentTab", () => ({
  __esModule: true,
  default: (props: { detail: unknown; type: string; names: string[] }) => (
    <div data-testid="segment-tab">
      {props.type}:{props.names.join(",")}
    </div>
  ),
}));

describe("SummaryTab", () => {
  it("delegates to SegmentTab with the given type as both type and names", () => {
    render(
      <SummaryTab
        detail={{ dataset_id: "ds-1", document_id: "doc-1" } as any}
        type="summary"
      />,
    );

    expect(screen.getByTestId("segment-tab")).toHaveTextContent("summary:summary");
  });

  it("forwards onGetItemInfo through to SegmentTab (implicitly via props)", () => {
    const onGetItemInfo = vi.fn();
    render(
      <SummaryTab
        detail={{ dataset_id: "ds-1", document_id: "doc-1" } as any}
        type="doc-summary"
        onGetItemInfo={onGetItemInfo}
      />,
    );

    expect(screen.getByTestId("segment-tab")).toHaveTextContent(
      "doc-summary:doc-summary",
    );
  });
});
