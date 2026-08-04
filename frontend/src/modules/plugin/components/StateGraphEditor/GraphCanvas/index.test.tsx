import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import GraphCanvas from "./index";
import { createEmptyModel } from "../core/model";

vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssets: vi.fn().mockResolvedValue([]),
}));

describe("GraphCanvas", () => {
  it("lazily loads and renders the Canvas component", async () => {
    renderWithProviders(
      <GraphCanvas model={createEmptyModel()} errors={[]} onModelChange={vi.fn()} />,
    );
    await waitFor(
      () => {
        expect(screen.getByText("selfEvolutionRun.stepNodeStart")).toBeInTheDocument();
      },
      { timeout: 10000 },
    );
    expect(screen.getByText("selfEvolutionRun.stepNodeEnd")).toBeInTheDocument();
  });
});
