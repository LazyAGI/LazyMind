import { useState } from "react";
import {
  act,
  render,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/react";
import { it, expect, vi, beforeEach } from "vitest";
import PaperReadingWorkspace from "./PaperReadingWorkspace";
import { revision, status } from "./PaperGuide/fixtures";
const state = vi.hoisted(() => ({ value: {} as Record<string, unknown> }));
const api = vi.hoisted(() => ({
  metadata: vi.fn().mockResolvedValue(undefined),
  generate: vi.fn(),
  cancel: vi.fn(),
  resume: vi.fn(),
  progress: vi.fn(),
  evidence: vi.fn(),
  prefs: vi.fn(),
}));
vi.mock("./PaperGuide/usePaperGuide", async (original) => ({
  ...(await original<typeof import("./PaperGuide/usePaperGuide")>()),
  usePaperGuide: () => state.value,
}));
vi.mock("../api/paperGuide", () => ({ paperGuideApi: api }));
vi.mock("../api/readingPreparation", () => ({
  getReadingPreferences: api.prefs,
}));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (e: Error) => e.message,
}));
vi.mock("react-i18next", async (original) => ({
  ...(await original<typeof import("react-i18next")>()),
  useTranslation: () => ({ i18n: { language: "en", t: (s: string) => s } }),
}));
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("paper-guide-mode/u/ds/doc", "source");
  vi.clearAllMocks();
  state.value = {
    status: status(),
    revision: revision(),
    identity: "u",
    refresh: vi.fn(),
    selectRevision: vi.fn(),
  };
  api.prefs.mockResolvedValue({
    provider_selections: {
      study: { options: [{ id: "own:run", label: "Per-run model" }] },
    },
  });
});
function Harness() {
  const [tab, setTab] = useState<string>();
  return (
    <PaperReadingWorkspace
      dataset="ds"
      document="doc"
      title="Paper"
      eligible
      source={<div>PDF viewer</div>}
      auxiliary={
        <div>
          Auxiliary {tab}
          <input aria-label="Persistent note" />
        </div>
      }
      auxiliaryOpen={!!tab}
      auxiliaryTab={tab || ""}
      onAuxiliary={setTab}
      back={<span>Back</span>}
      dock={null}
      getPdfData={() => null}
      onSelection={() => {}}
      onNavigate={() => {}}
    />
  );
}
it("defaults to original then keeps source and assistant mutually exclusive at narrow widths", async () => {
  render(<Harness />);
  expect(screen.getByText("PDF viewer")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  await screen.findByRole("article");
  fireEvent.click(screen.getByRole("button", { name: "Compare original" }));
  expect(screen.getByText("PDF viewer")).toBeVisible();
  expect(screen.queryByRole("article")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Assistant" }));
  expect(screen.getByText("PDF viewer")).not.toBeVisible();
  expect(screen.getByLabelText("Persistent note")).toBeVisible();
  fireEvent.change(screen.getByLabelText("Persistent note"), {
    target: { value: "keep" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Compare original" }));
  fireEvent.click(screen.getByRole("button", { name: "Notes" }));
  expect(screen.getByLabelText("Persistent note")).toHaveValue("keep");
});
it("regenerates with a per-run model and preserves the opened old article", async () => {
  render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  await screen.findByRole("option", { name: "Per-run model" });
  fireEvent.click(screen.getByText("Generation details"));
  fireEvent.change(
    screen.getByRole("combobox", { name: "Model for this run" }),
    { target: { value: "own:run" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
  await waitFor(() =>
    expect(api.generate).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({
        model_ref: "own:run",
        regenerate: true,
        expected_source_hash: "hash",
      }),
    ),
  );
  expect(screen.getByRole("article")).toBeInTheDocument();
});
it("shows already published content and cancellation while a later candidate generates", async () => {
  state.value = {
    ...state.value,
    status: {
      ...status(),
      candidate_revision: {
        ...revision(),
        id: "r2",
        run: { ...revision().run, stage: "writing" },
      },
    },
  };
  render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  expect(screen.getByRole("article")).toBeInTheDocument();
  await act(async () =>
    fireEvent.click(
      screen.getByRole("button", { name: "Cancel remaining generation" }),
    ),
  );
  expect(api.cancel).toHaveBeenCalledWith(expect.anything(), "r2");
});
it("hides creation entry when capability is off but still exposes authorized existing guides", () => {
  state.value = {
    ...state.value,
    status: {
      ...status(),
      enabled: false,
      published_revision: null,
      candidate_revision: null,
    },
    revision: undefined,
  };
  const view = render(<Harness />);
  expect(
    screen.queryByRole("button", { name: "Guide" }),
  ).not.toBeInTheDocument();
  state.value = {
    ...state.value,
    status: { ...status(), enabled: false, can_generate: false },
    revision: revision(),
  };
  view.rerender(<Harness />);
  expect(screen.getByRole("button", { name: "Guide" })).toBeInTheDocument();
});

it('shows linked author and year metadata without promoting an abstract to guide text',async()=>{api.metadata.mockResolvedValueOnce({title:'Actual paper title',authors:['A. Author'],year:2026});render(<Harness/>);fireEvent.click(screen.getByRole('button',{name:'Guide'}));expect(await screen.findByRole('heading',{name:'Actual paper title'})).toBeInTheDocument();expect(screen.getByText('A. Author · 2026')).toBeInTheDocument();});
