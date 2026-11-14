import { useState } from "react";
import {
  act,
  render,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/react";
import { it, expect, vi, beforeEach, afterEach } from "vitest";
import { axiosInstance } from "@/components/request";
import PaperReadingWorkspace from "./PaperReadingWorkspace";
import { revision, status } from "./PaperGuide/fixtures";
const state = vi.hoisted(() => ({ value: {} as Record<string, unknown> }));
const api = vi.hoisted(() => ({
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
vi.mock("../api/paperGuide", async (original) => ({
  paperGuideApi: {
    ...(await original<typeof import("../api/paperGuide")>()).paperGuideApi,
    ...api,
  },
}));
vi.mock("../api/readingPreparation", () => ({
  getReadingPreferences: api.prefs,
}));
vi.mock("@/components/request", async (original) => ({
  ...(await original<typeof import("@/components/request")>()),
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
  vi.spyOn(axiosInstance, "get").mockRejectedValue(
    new Error("Unexpected GET"),
  );
  state.value = {
    status: status(),
    revision: revision(),
    identity: "u",
    refresh: vi.fn().mockResolvedValue(undefined),
    selectRevision: vi.fn(),
  };
  api.prefs.mockResolvedValue({
    provider_selections: {
      study: { options: [{ id: "own:run", label: "Per-run model" }] },
    },
  });
});
afterEach(() => vi.restoreAllMocks());

function Harness({
  document = "doc",
  title = "Paper",
}: {
  document?: string;
  title?: string;
}) {
  const [tab, setTab] = useState<string>();
  return (
    <PaperReadingWorkspace
      dataset="ds"
      document={document}
      title={title}
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

it("keeps the document heading through delayed academic metadata and checkpoint appends", async () => {
  const title =
    "Angew Chem Int Ed - 2023 - Poh - Bifunctional Iminophosphorane Catalyzed Amide Enolization for Enantioselective.pdf";
  const libraryResponse = {
    data: {
      can_write: true,
      data: {
        papers: [
          {
            id: "bimp",
            revision: 3,
            metadata: {
              title: "Communications2323",
              authors: ["Charmaine Y. X. Poh"],
              year: 2015,
              identifiers: { doi: "10.1002/anie.202315401" },
            },
            tags: [],
            attachmentIds: ["attachment"],
            noteIds: [],
          },
        ],
        attachments: [{ id: "attachment", paperId: "bimp", contentRef: "doc" }],
        folders: [],
        notes: [],
      },
    },
  };
  let resolve!: (response: typeof libraryResponse) => void;
  const pendingLibrary = new Promise<typeof libraryResponse>((done) => {
    resolve = done;
  });
  const request = vi.mocked(axiosInstance.get).mockImplementation((url) => {
    if (url.endsWith("/api/core/academic/library")) return pendingLibrary;
    throw new Error(`Unexpected GET: ${url}`);
  });
  const published = revision();
  state.value.revision = {
    ...published,
    sections: published.sections.slice(0, 3),
  };
  const view = render(<Harness title={title} />);
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "Guide" })),
  );
  const heading = screen.getByRole("heading", { level: 1, name: title });
  await act(async () => resolve(libraryResponse));
  expect(heading).toHaveTextContent(title);
  expect(screen.getByRole("article")).not.toHaveTextContent(
    /Communications2323|Charmaine Y\. X\. Poh|2015/,
  );
  state.value.revision = published;
  view.rerender(<Harness title={title} />);
  expect(
    screen.getByRole("heading", { name: "Comparisons under matched conditions" }),
  ).toBeVisible();
  expect(screen.getByRole("heading", { level: 1, name: title })).toBe(heading);
  expect(request).not.toHaveBeenCalled();
});

it("uses each document's own heading while guide content loads and across original mode", async () => {
  state.value.revision = undefined;
  const view = render(<Harness title="First document.pdf" />);
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "Guide" })),
  );
  expect(
    screen.getByRole("heading", { level: 1, name: "First document.pdf" }),
  ).toBeVisible();
  expect(screen.queryByRole("article")).not.toBeInTheDocument();
  state.value.revision = revision();
  view.rerender(<Harness title="First document.pdf" />);
  expect(screen.getByRole("article")).toContainElement(
    screen.getByRole("heading", { level: 1, name: "First document.pdf" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Original" }));
  expect(screen.getByText("PDF viewer")).toBeVisible();
  expect(screen.queryByRole("heading", { level: 1 })).not.toBeInTheDocument();
  localStorage.setItem("paper-guide-mode/u/ds/doc2", "source");
  state.value.revision = revision("theory");
  view.rerender(<Harness document="doc2" title="Second document.pdf" />);
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "Guide" })),
  );
  expect(
    screen.getByRole("heading", { level: 1, name: "Second document.pdf" }),
  ).toBeVisible();
  expect(screen.getByRole("heading", { name: "A question of convergence" })).toBeVisible();
  expect(
    screen.queryByRole("heading", { name: "First document.pdf" }),
  ).not.toBeInTheDocument();
});
it("reserves an inline figure frame before evidence arrives and retains it on failure", async () => {
  let reject!: (reason: Error) => void;
  api.evidence.mockImplementationOnce(
    () =>
      new Promise((_, r) => {
        reject = r;
      }),
  );
  const r = revision();
  r.sections[0].blocks[0].asset_id = "figure-evidence";
  state.value = { ...state.value, revision: r };
  const view = render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  const frame = view.container.querySelector(".paper-guide-image-frame");
  expect(frame).not.toBeNull();
  expect(screen.getByText("Loading original figure…")).toBeVisible();
  await act(async () => reject(new Error("evidence unavailable")));
  expect(view.container.querySelector(".paper-guide-image-frame")).toBe(frame);
  expect(
    screen.getByText(
      "Preview unavailable. Open the PDF to inspect the original.",
    ),
  ).toBeVisible();
});
it("keeps the same reserved figure and caption rows through delayed evidence and PDF rendering", async () => {
  let resolve!: (value: unknown) => void;
  api.evidence.mockImplementationOnce(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  const r = revision();
  r.sections[0].blocks[0].asset_id = "figure-evidence";
  state.value = { ...state.value, revision: r };
  const view = render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  const frame = view.container.querySelector(".paper-guide-image-frame");
  const caption = view.container.querySelector(".paper-guide-asset figcaption");
  expect(frame).not.toBeNull();
  expect(caption).not.toBeNull();
  await act(async () =>
    resolve({
      source_current: true,
      evidence: {
        id: "figure-evidence",
        text: "Original figure",
        source_block_id: "source",
        source_type: "image",
        locator: { kind: "bbox", page: 2 },
      },
    }),
  );
  expect(view.container.querySelector(".paper-guide-image-frame")).toBe(frame);
  expect(view.container.querySelector(".paper-guide-asset figcaption")).toBe(
    caption,
  );
  expect(caption).toHaveTextContent("PDF 3");
});

function pausedCandidate(
  errorCode = "paper_guide_invalid_evidence",
  id = "r2",
) {
  return {
    ...revision(),
    id,
    sections: [],
    run: { ...revision().run, id, stage: "paused", error_code: errorCode },
  };
}
function showPaused(errorCode?: string, empty = false) {
  state.value = {
    ...state.value,
    status: { ...status(), candidate_revision: pausedCandidate(errorCode) },
    ...(empty ? { revision: undefined } : {}),
  };
}
const regenerationError = () =>
  Object.assign(new Error("regeneration required"), {
    response: { status: 409, data: { code: 2004413 } },
  });

it("replaces incompatible resume with an explicit new revision while preserving the open article", async () => {
  showPaused("paper_guide_regeneration_required");
  render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  expect(
    screen.queryByRole("button", { name: "Resume" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("errors.2004413");
  await screen.findByRole("option", { name: "Per-run model" });
  fireEvent.change(screen.getByLabelText("Model for this run"), {
    target: { value: "own:run" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Generate a new guide" }));
  await waitFor(() =>
    expect(api.generate).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({
        regenerate: true,
        expected_source_hash: "hash",
        model_ref: "own:run",
      }),
    ),
  );
  expect(screen.getByRole("article")).toBeVisible();
});
it("remembers a rejected resume immediately through stale status and failed refresh", async () => {
  showPaused();
  api.resume.mockRejectedValueOnce(regenerationError());
  state.value.refresh = vi.fn().mockRejectedValue(new Error("offline"));
  const view = render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  fireEvent.click(screen.getByRole("button", { name: "Resume" }));
  await screen.findByRole("button", { name: "Generate a new guide" });
  expect(
    screen.queryByRole("button", { name: "Resume" }),
  ).not.toBeInTheDocument();
  view.rerender(<Harness />);
  expect(screen.getByRole("status")).toHaveTextContent("errors.2004413");
  expect(api.resume).toHaveBeenCalledTimes(1);
});
it.each(["paper_guide_invalid_evidence", "paper_guide_regeneration_required"])(
  "never offers initial generation for an empty paused %s candidate",
  async (code) => {
    showPaused(code, true);
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Guide" }));
    expect(
      screen.queryByRole("button", {
        name: /Prepare paper and generate guide|Generate paper guide/,
      }),
    ).not.toBeInTheDocument();
    const incompatible = code === "paper_guide_regeneration_required";
    fireEvent.click(
      screen.getByRole("button", {
        name: incompatible ? "Generate a new guide" : "Resume",
      }),
    );
    await waitFor(() =>
      incompatible
        ? expect(api.generate).toHaveBeenCalledWith(
            expect.anything(),
            expect.objectContaining({ regenerate: true }),
          )
        : expect(api.resume).toHaveBeenCalledWith(expect.anything(), "r2"),
    );
    if (!incompatible) expect(api.generate).not.toHaveBeenCalled();
  },
);
it("does not treat unrelated conflicts as an incompatible generation format", async () => {
  showPaused();
  api.resume.mockRejectedValueOnce(
    Object.assign(new Error("source changed"), {
      response: { status: 409, data: { code: 2004401 } },
    }),
  );
  render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  fireEvent.click(screen.getByRole("button", { name: "Resume" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
  expect(screen.getByRole("status")).not.toHaveTextContent("errors.2004413");
});
it.each(["candidate", "document", "identity"])(
  "does not apply a late resume rejection after switching %s",
  async (scope) => {
    showPaused();
    let reject!: (reason: unknown) => void;
    api.resume.mockImplementationOnce(
      () =>
        new Promise((_, r) => {
          reject = r;
        }),
    );
    const view = render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Guide" }));
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    if (scope === "candidate")
      state.value = {
        ...state.value,
        status: {
          ...status(),
          candidate_revision: pausedCandidate(undefined, "r3"),
        },
      };
    if (scope === "identity")
      state.value = { ...state.value, identity: "other" };
    view.rerender(<Harness document={scope === "document" ? "doc2" : "doc"} />);
    await act(async () => reject(regenerationError()));
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "Guide" })),
    );
    expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).not.toHaveTextContent("errors.2004413");
  },
);

it.each(["candidate", "document", "identity"])(
  "clears a known incompatible resume when switching %s",
  async (scope) => {
    showPaused();
    api.resume.mockRejectedValueOnce(regenerationError());
    const view = render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Guide" }));
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "Resume" }),
      ).not.toBeInTheDocument(),
    );
    if (scope === "candidate")
      state.value = {
        ...state.value,
        status: {
          ...status(),
          candidate_revision: pausedCandidate(undefined, "r3"),
        },
      };
    if (scope === "identity")
      state.value = { ...state.value, identity: "other" };
    view.rerender(<Harness document={scope === "document" ? "doc2" : "doc"} />);
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "Guide" })),
    );
    expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
    expect(screen.getByRole("status")).not.toHaveTextContent("errors.2004413");
  },
);

it.each([
  ["paper_guide_model_timeout", "errors.2004414"],
  ["paper_guide_model_request_failed", "errors.2004415"],
])(
  "explains %s without losing the published article",
  async (code, message) => {
    showPaused(code);
    render(<Harness />);
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "Guide" })),
    );
    expect(screen.getByRole("status")).toHaveTextContent(message);
    expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
    expect(screen.getByRole("article")).toBeVisible();
  },
);

it("explains unavailable regeneration while a historical guide remains readable", async () => {
  state.value = { ...state.value, status: { ...status(), can_generate: false, unavailable_reason: "paper_guide_source_precision_unavailable" }, revision: { ...revision(), source_current: false } };
  render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  fireEvent.click(screen.getByText("Generation details"));
  const button = screen.getByRole("button", { name: "Regenerate" });
  expect(button).toBeDisabled();
  expect(button).toHaveAccessibleDescription(/Source pages could not be verified/);
  expect(screen.getByText(/original paper and historical guides remain readable/)).toBeVisible();
  expect(screen.queryByText("paper_guide_source_precision_unavailable")).not.toBeInTheDocument();
});
