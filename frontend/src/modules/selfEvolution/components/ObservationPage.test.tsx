import { describe, expect, it, vi } from "vitest";

const { fetchThreadGateContent } = vi.hoisted(() => ({ fetchThreadGateContent: vi.fn() }));

vi.mock("../shared", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../shared")>();
  return {
    ...actual,
    fetchThreadGateContent,
  };
});

import { render, screen, waitFor } from "@testing-library/react";
import { I18nextProvider } from "react-i18next";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { testI18n } from "@/test/testUtils";
import { SelfEvolutionObservationPage } from "./ObservationPage";

// SelfEvolutionObservationPage reads menu state via useOutletContext, which requires
// an actual parent Outlet in the route tree (not just a wrapper component).
function renderWithOutletContext(
  path: string,
  outletContext: { isMenuCollapsed?: boolean; toggleMenu?: () => void } = {},
) {
  function Layout() {
    return <Outlet context={outletContext} />;
  }
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route element={<Layout />}>
            <Route
              path="/self-evolution/observation/:threadId/:kind"
              element={<SelfEvolutionObservationPage />}
            />
          </Route>
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("SelfEvolutionObservationPage", () => {
  it("shows an unknown observation type warning for an unsupported kind", async () => {
    fetchThreadGateContent.mockReset();
    renderWithOutletContext("/self-evolution/observation/thread-1/unknown-kind");
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.observation.unknownObservationType")).toBeInTheDocument();
    });
  });

  it("renders the eval report dashboard when kind is eval and data loads", async () => {
    fetchThreadGateContent.mockReset();
    fetchThreadGateContent.mockResolvedValue({
      run_id: "run-1",
      algo_id: "algo-a",
      avg_correctness: 0.9,
      case_num: 0,
      cases: [],
    });
    renderWithOutletContext("/self-evolution/observation/thread-1/eval");
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.observation.evalReportTitle")).toBeInTheDocument();
    });
  });

  it("renders the raw data fallback for abtests with no rows and no comparison artifact", async () => {
    fetchThreadGateContent.mockReset();
    fetchThreadGateContent.mockResolvedValue({ foo: "bar" });
    renderWithOutletContext("/self-evolution/observation/thread-1/abtest");
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.observation.rawData")).toBeInTheDocument();
    });
  });

  it("renders the abtest dashboard's thread tag once data for that thread loads", async () => {
    fetchThreadGateContent.mockReset();
    fetchThreadGateContent.mockResolvedValue({ foo: "bar" });
    renderWithOutletContext("/self-evolution/observation/thread-77/abtest");
    await waitFor(() => {
      expect(screen.getByText("thread thread-77")).toBeInTheDocument();
    });
  });
});
