import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SLOT_TYPE_ICONS } from "./slotTypeIcon";

describe("SLOT_TYPE_ICONS", () => {
  it("provides an icon for every known slot type", () => {
    expect(Object.keys(SLOT_TYPE_ICONS).sort()).toEqual(["file", "image", "json", "text"]);
  });

  it("renders a distinct icon element for each slot type", () => {
    for (const [type, icon] of Object.entries(SLOT_TYPE_ICONS)) {
      const { container } = render(<>{icon}</>);
      expect(container.querySelector("svg"), `icon for ${type}`).toBeInTheDocument();
    }
  });
});
