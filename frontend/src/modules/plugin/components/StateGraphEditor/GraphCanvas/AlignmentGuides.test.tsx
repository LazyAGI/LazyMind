import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ReactFlowProvider } from "@xyflow/react";
import { AlignmentGuides } from "./AlignmentGuides";
import type { GuideLine } from "./useAlignmentGuides";

describe("AlignmentGuides", () => {
  it("renders nothing when there are no guides", () => {
    const { container } = render(
      <ReactFlowProvider>
        <AlignmentGuides guides={[]} />
      </ReactFlowProvider>,
    );
    expect(container.querySelector("svg")).toBeNull();
  });

  it("renders a line element for each guide", () => {
    const guides: GuideLine[] = [
      { type: "horizontal", y: 100, x1: 0, x2: 200 },
      { type: "vertical", x: 50, y1: 0, y2: 150 },
    ];
    const { container } = render(
      <ReactFlowProvider>
        <AlignmentGuides guides={guides} />
      </ReactFlowProvider>,
    );
    expect(container.querySelectorAll("line")).toHaveLength(2);
  });

  it("uses a distinct stroke color for symmetric guides", () => {
    const guides: GuideLine[] = [
      { type: "vertical", x: 50, y1: 0, y2: 150, symmetric: true } as GuideLine,
    ];
    const { container } = render(
      <ReactFlowProvider>
        <AlignmentGuides guides={guides} />
      </ReactFlowProvider>,
    );
    const line = container.querySelector("line");
    expect(line).toHaveAttribute("stroke", "#722ed1");
  });
});
