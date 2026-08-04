import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import DefaultServicesPage from "./DefaultServicesPage";

vi.mock("../components/DefaultModelConfigPanel", () => ({
  default: () => <div data-testid="default-model-config-panel" />,
}));

describe("DefaultServicesPage", () => {
  it("renders the DefaultModelConfigPanel inside the service page wrapper", () => {
    const { container } = renderWithProviders(<DefaultServicesPage />);
    expect(screen.getByTestId("default-model-config-panel")).toBeInTheDocument();
    expect(
      container.querySelector(".model-provider-service-page"),
    ).not.toBeNull();
  });
});
