import { describe, expect, it } from "vitest";
import { fireEvent, renderWithProviders } from "@/test/testUtils";
import RiskTip from "./RiskTip";

describe("RiskTip", () => {
  it("renders the info icon without crashing", () => {
    const { container } = renderWithProviders(<RiskTip titleKey="risk.someKey" />);
    expect(container.querySelector(".anticon-info-circle")).not.toBeNull();
  });

  it("shows the translated (key) tooltip content when hovered", async () => {
    const { container, findByText } = renderWithProviders(
      <RiskTip titleKey="risk.someKey" />,
    );
    const icon = container.querySelector(".anticon-info-circle") as HTMLElement;
    fireEvent.mouseEnter(icon);
    expect(await findByText("risk.someKey")).toBeTruthy();
  });
});
