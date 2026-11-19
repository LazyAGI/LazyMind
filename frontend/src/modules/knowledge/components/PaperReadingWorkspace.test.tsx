import { useState } from "react";
import { flushSync } from "react-dom";
import { createHash } from "node:crypto";
import { jsPDF } from "jspdf";
import { ConfigProvider } from "antd";
import {
  act,
  render,
  screen,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react";
import { it, expect, vi, beforeEach, afterEach } from "vitest";
import { axiosInstance } from "@/components/request";
import PaperReadingWorkspace from "./PaperReadingWorkspace";
import { revision, status } from "./PaperGuide/fixtures";
vi.mock("react-pdf", () => ({
  pdfjs: {
    getDocument: () => ({ promise: Promise.resolve({
      getPage: () => Promise.reject(new Error("Synthetic preview render failure")),
      destroy: () => Promise.resolve(),
    }) }),
  },
}));
const state = vi.hoisted(() => ({ value: {} as Record<string, unknown>, read: vi.fn() }));
const locale = vi.hoisted(() => ({ language: "en" }));
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
  usePaperGuide: (...args: unknown[]) => { state.read(...args); return state.value; },
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
  useTranslation: () => ({ i18n: { language: locale.language, t: (s: string) => s } }),
}));
beforeEach(() => {
  locale.language = "en";
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

async function chooseOption(label: string, option: string) {
  fireEvent.mouseDown(screen.getByRole("combobox", { name: label }));
  fireEvent.click(await screen.findByRole("option", { name: option }));
}

function openGuide(name = "Guide") {
  flushSync(() => fireEvent.click(screen.getByRole("button", { name })));
  const details = screen.queryByRole("button", { name: /Generation status and actions|生成状态与操作/ });
  if (details) fireEvent.click(details);
}

const noPdfData = () => null;
const fixturePdf = new jsPDF();
fixturePdf.text("A source equation.", 10, 10);
const fixturePdfData = fixturePdf.output("arraybuffer");
const fixturePdfHash = createHash("sha256").update(new Uint8Array(fixturePdfData)).digest("hex");
const getFixturePdf = () => fixturePdfData.slice(0);
const getDifferentPdf = () => Uint8Array.of(1,2,3,4).buffer;

function Harness({
  inlineSwitch = false,
  document = "doc",
  title = "Paper",
  onNavigate = () => {},
  withIR = false,
  getPdfData = noPdfData,
  getPdfVersion,
  sourceHash = "hash",
}: {
  inlineSwitch?: boolean;
  document?: string;
  title?: string;
  onNavigate?: Parameters<typeof PaperReadingWorkspace>[0]["onNavigate"];
  withIR?: boolean;
  getPdfData?: () => ArrayBuffer | null;
  getPdfVersion?: () => object | null;
  sourceHash?: string;
}) {
  const [tab, setTab] = useState<string>();
  return (
    <PaperReadingWorkspace
      dataset="ds"
      document={document}
      title={title}
      eligible
      source={inlineSwitch ? control => <div><nav aria-label="PDF tools">{control}<button>Zoom</button></nav><div>PDF viewer</div></div> : <div>PDF viewer</div>}
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
      getPdfData={getPdfData}
      getPdfVersion={getPdfVersion}
      onSelection={() => {}}
      onNavigate={onNavigate}
      ir={withIR ? {
        version: 1,
        source_sha256: sourceHash,
        parser: { name: "fixture", model: "fixture" },
        coordinate_space: "normalized_top_left",
        pages: [],
        blocks: [],
      } : undefined}
    />
  );
}

function SourcePreviewHarness(props: Parameters<typeof Harness>[0]) {
  return (
    <ConfigProvider theme={{ token: { motion: false } }}>
      <Harness {...props} withIR />
    </ConfigProvider>
  );
}

async function openSourcePreview() {
  api.evidence.mockResolvedValueOnce({
    source_current: true,
    evidence: {
      id: "e1",
      text: "Retained source evidence",
      source_block_id: "source",
      source_type: "body",
      locator: { kind: "page", page: 2 },
    },
  });
  openGuide("Guide");
  const opener = screen.getByRole("button", { name: "Source 1" });
  vi.spyOn(opener, "getClientRects").mockReturnValue({ length: 1 } as DOMRectList);
  // Pointer activation need not focus the button (e.g. macOS Safari).
  fireEvent.click(opener);
  await screen.findByText("Retained source evidence");
  return opener;
}

it.each(["button", "Escape"])("returns focus to the source citation after closing with %s", async (action) => {
  render(<SourcePreviewHarness />);
  const opener = await openSourcePreview();
  const close = screen.getByRole("button", { name: "Close source" });
  close.focus();
  if (action === "Escape") {
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape", keyCode: 27 });
  } else {
    fireEvent.click(close);
  }
  await waitFor(() => expect(opener).toHaveFocus());
});

it("does not restore a source opener that is no longer visible", async () => {
  render(<SourcePreviewHarness />);
  const opener = await openSourcePreview();
  vi.mocked(opener.getClientRects).mockReturnValue({ length: 0 } as DOMRectList);
  const focus = vi.spyOn(opener, "focus");
  fireEvent.click(screen.getByRole("button", { name: "Close source" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(focus).not.toHaveBeenCalled();
});

it("leaves focus at the destination when navigating from the source preview", async () => {
  const navigate = vi.fn(() => screen.getByRole("button", { name: "Original" }).focus());
  render(<SourcePreviewHarness onNavigate={navigate} />);
  const opener = await openSourcePreview();
  const focus = vi.spyOn(opener, "focus");
  fireEvent.click(screen.getByRole("button", { name: "Open PDF page 3" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(navigate).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Original" })).toHaveFocus();
  expect(focus).not.toHaveBeenCalled();
});

it("keeps an open preview readable but disables its jump when refreshed source positioning is stale", async () => {
  const view = render(<SourcePreviewHarness />);
  await openSourcePreview();
  expect(screen.getByRole("button", { name: "Open PDF page 3" })).toBeEnabled();
  state.value.revision = { ...revision(), source_current: false };
  view.rerender(<SourcePreviewHarness />);
  expect(screen.getByText("Retained source evidence")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Open PDF page 3" })).toBeDisabled();
});
it("shows the mode switch with the PDF toolbar before guide status arrives", async () => {
  const ready = state.value;
  state.value = { ...ready, status: undefined, revision: undefined, loading: true };
  const view = render(<Harness inlineSwitch />);
  const toolbar = screen.getByRole("navigation", { name: "PDF tools" });
  expect(within(toolbar).getByRole("button", { name: "Original" })).toHaveAttribute("aria-pressed", "true");
  fireEvent.click(within(toolbar).getByRole("button", { name: "Guide" }));
  expect(screen.getByRole("button", { name: "Guide" })).toHaveAttribute("aria-pressed", "true");
  state.value = ready;
  view.rerender(<Harness inlineSwitch />);
  expect(await screen.findByRole("article")).toBeVisible();
});

it("restores a saved guide before status arrives and mounts the original only on request", async () => {
  localStorage.setItem("paper-guide-mode/u/ds/doc", "article");
  const ready = state.value;
  state.value = { ...ready, status: undefined, revision: undefined, loading: true };
  const view = render(<Harness inlineSwitch />);
  expect(screen.queryByText("PDF viewer")).toBeNull();
  expect(screen.queryByText("Auxiliary")).toBeNull();
  expect(screen.getByRole("button", { name: "Guide" })).toHaveAttribute("aria-pressed", "true");
  state.value = ready;
  view.rerender(<Harness inlineSwitch />);
  expect(await screen.findByRole("article")).toBeVisible();
  expect(screen.queryByText("PDF viewer")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Original" }));
  expect(screen.getByText("PDF viewer")).toBeVisible();
});

it("opens the original if a restored guide is no longer readable", () => {
  localStorage.setItem("paper-guide-mode/u/ds/doc", "article");
  state.value = { ...state.value, status: undefined, revision: undefined };
  const view = render(<Harness inlineSwitch />);
  expect(screen.queryByText("PDF viewer")).toBeNull();
  state.value.status = { enabled: false, can_read: false };
  view.rerender(<Harness inlineSwitch />);
  expect(screen.getByText("PDF viewer")).toBeVisible();
});

it("switches with a stable two-button toolbar and preserves the mounted PDF", async () => {
  render(<Harness />);
  const pdf = screen.getByText("PDF viewer");
  const modes = screen.getByRole("group", { name: "Reading mode" });
  expect(pdf).toBeVisible();
  openGuide("Guide");
  expect(await screen.findByRole("article")).toBeVisible();
  expect(pdf).not.toBeVisible();
  expect(modes.querySelectorAll("button")).toHaveLength(2);
  for (const name of ["Compare original", "Assistant", "Notes", "Translation"]) {
    expect(screen.queryByRole("button", { name })).toBeNull();
  }
  expect(screen.queryByText("Generation details")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Original" }));
  expect(screen.getByText("PDF viewer")).toBe(pdf);
  expect(pdf).toBeVisible();
  expect(screen.getByRole("group", { name: "Reading mode" })).toBe(modes);
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
  openGuide("Guide");
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
    openGuide("Guide"),
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
    openGuide("Guide"),
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
    openGuide("Guide"),
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
  openGuide("Guide");
  const frame = view.container.querySelector(".paper-guide-image-frame");
  expect(frame).not.toBeNull();
  expect(screen.getByText("Loading original…")).toBeVisible();
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
  openGuide("Guide");
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
  expect(caption).toHaveTextContent("PDF page 3");
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
const qualityCode = "paper_guide_review_required";
const qualityHelp = "This revision can’t continue. Generate a new guide.";
const qualityHelpZh = "当前版本无法继续生成，请重新生成导读。";

it.each([
  { stage: "paused", language: "en", projected: true, canGenerate: true },
  { stage: "canceled", language: "zh", projected: false, canGenerate: false },
])("keeps four readable sections and blocks resume for a $stage quality stop", async ({
  stage, language, projected, canGenerate,
}) => {
  locale.language = language;
  const article = revision();
  article.sections[1] = {
    ...article.sections[1], key: "section:question:v2", version: 2,
    publication_status: "correction_required",
  };
  const candidate = {
    ...article, id: "r2", status: "partial",
    run: {
      ...article.run, id: "r2", stage,
      ...(projected ? { resume_block_reason: qualityCode } : { error_code: qualityCode }),
    },
  };
  state.value = {
    ...state.value,
    status: { ...status(), can_generate: canGenerate, candidate_revision: candidate },
    revision: candidate,
  };
  render(<Harness />);
  await act(async () =>
    openGuide(language === "zh" ? "导读" : "Guide"),
  );
  expect(screen.queryByRole("button", { name: /^(Resume|继续生成)$/ })).not.toBeInTheDocument();
  expect(screen.getByText(language === "zh" ? qualityHelpZh : qualityHelp)).toBeInTheDocument();
  for (const section of article.sections)
    expect(screen.getByRole("heading", { name: section.title })).toBeVisible();
  expect(screen.getByText(language === "zh" ? /待更正/ : "Correction pending")).toBeVisible();
  const regenerate = screen.getByRole("button", { name: language === "zh" ? "重新生成导读" : "Generate a new guide" });
  if (canGenerate) {
    expect(regenerate).toBeEnabled();
    await act(async () => fireEvent.click(regenerate));
    await act(async () => fireEvent.click(screen.getByRole("button", { name: language === "zh" ? "开始重新生成" : "Start regeneration" })));
    expect(api.generate).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ regenerate: true }));
  } else {
    expect(regenerate).toBeDisabled();
  }
});

it("offers regeneration after the source changes during the first generation", async () => {
  const stopped = pausedCandidate("paper_guide_source_changed");
  stopped.run.stage = "source_changed";
  state.value = {
    ...state.value,
    status: { ...status(), published_revision: null, candidate_revision: stopped },
    revision: stopped,
  };
  render(<Harness />);
  await act(async () => openGuide("Guide"));
  expect(screen.queryByRole("button", { name: "Resume" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Generate a new guide" }));
  fireEvent.click(screen.getByRole("button", { name: "Start regeneration" }));
  await waitFor(() => expect(api.generate).toHaveBeenCalledWith(
    expect.anything(), expect.objectContaining({ regenerate: true, expected_source_hash: "hash" }),
  ));
});

it("shows the old-contract explanation when the candidate also has a saved quality stop", async () => {
  const candidate = pausedCandidate("paper_guide_regeneration_required");
  state.value.status = {
    ...status(),
    candidate_revision: {
      ...candidate,
      run: { ...candidate.run, resume_block_reason: qualityCode },
    },
  };
  render(<Harness />);
  await act(async () => openGuide("Guide"));
  expect(screen.getByText("errors.2004413")).toBeInTheDocument();
  expect(screen.queryByText(qualityHelp)).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Resume" })).not.toBeInTheDocument();
});

it.each([
  { stage: "canceled" },
  { stage: "paused", error_code: "paper_guide_model_timeout" },
  { stage: "paused", error_code: "paper_guide_model_request_failed" },
])("continues offering resume for $stage $error_code", async (run) => {
  const candidate = pausedCandidate();
  state.value.status = {
    ...status(),
    candidate_revision: { ...candidate, run: { ...candidate.run, error_code: undefined, ...run } },
  };
  render(<Harness />);
  await act(async () => openGuide("Guide"));
  expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
});

it("keeps a stale candidate blocked after receiving the quality 422 and refreshes", async () => {
  showPaused();
  api.resume.mockRejectedValueOnce(Object.assign(new Error("quality rejected"), {
    response: { status: 422, data: { code: 2004411, data: { error_code: qualityCode } } },
  }));
  state.value.refresh = vi.fn().mockRejectedValue(new Error("offline"));
  const view = render(<Harness />);
  await act(async () => openGuide("Guide"));
  fireEvent.click(screen.getByRole("button", { name: "Resume" }));
  await screen.findByText(qualityHelp);
  expect(state.value.refresh).toHaveBeenCalledTimes(1);
  view.rerender(<Harness />);
  expect(screen.queryByRole("button", { name: "Resume" })).not.toBeInTheDocument();
  expect(screen.getByRole("article")).toBeVisible();
  expect(screen.getByRole("button", { name: "Generate a new guide" })).toBeEnabled();
});

it("keeps resume available after an unrelated 422", async () => {
  showPaused();
  api.resume.mockRejectedValueOnce(Object.assign(new Error("model unavailable"), {
    response: { status: 422, data: { code: 2004403 } },
  }));
  render(<Harness />);
  await act(async () => openGuide("Guide"));
  fireEvent.click(screen.getByRole("button", { name: "Resume" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("model unavailable");
  expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
  expect(screen.queryByText(qualityHelp)).not.toBeInTheDocument();
});

it("replaces incompatible resume with an explicit new revision while preserving the open article", async () => {
  showPaused("paper_guide_regeneration_required");
  render(<Harness />);
  openGuide("Guide");
  expect(
    screen.queryByRole("button", { name: "Resume" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("errors.2004413");
  expect(screen.queryByRole("combobox", { name: "Model for this run" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Generate a new guide" }));
  await chooseOption("Model for this run", "Per-run model");
  fireEvent.click(screen.getByRole("button", { name: "Start regeneration" }));
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
  openGuide("Guide");
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
    openGuide("Guide");
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
    if (incompatible) fireEvent.click(screen.getByRole("button", { name: "Start regeneration" }));
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
  openGuide("Guide");
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
    openGuide("Guide");
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
      openGuide("Guide"),
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
    openGuide("Guide");
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
      openGuide("Guide"),
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
      openGuide("Guide"),
    );
    expect(screen.getByRole("status")).toHaveTextContent(message);
    expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
    expect(screen.getByRole("article")).toBeVisible();
  },
);

it("explains unavailable regeneration while a historical guide remains readable", async () => {
  state.value = { ...state.value, status: { ...status(), can_generate: false, unavailable_reason: "paper_guide_source_precision_unavailable" }, revision: { ...revision(), source_current: false } };
  render(<Harness />);
  openGuide("Guide");
  expect(screen.getByRole("article")).toBeVisible();
  expect(screen.queryByText("Generation details")).toBeNull();
  expect(screen.queryByText("paper_guide_source_precision_unavailable")).not.toBeInTheDocument();
});

it("opens the authorized original page from the formula block and hides an older request on revision change", async () => {
  const navigate = vi.fn();
  const old = { ...revision(), source_sha256: fixturePdfHash };
  old.sections[0].blocks[0] = { ...old.sections[0].blocks[0], kind: "formula",
    asset_id: "old-formula", text: "Compare $x=y$ in the guide." };
  let finishOld!: (response: unknown) => void;
  api.evidence.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; }));
  state.value = { ...state.value, revision: old };
  const view = render(<Harness withIR onNavigate={navigate} sourceHash={fixturePdfHash} getPdfData={getFixturePdf} />);
  openGuide("Guide");
  const formula = { id: "new-formula", text: "x=y", source_block_id: "original-formula", source_type: "formula",
    locator: { kind: "formula", page: 3, bbox: [.1, .2, .3, .4], latex: "x=y" } };
  api.evidence.mockResolvedValueOnce({ evidence: formula, source_current: true,
    original_display: { kind: "formula", page: 3, bbox: [.1, .2, .3, .4], number: "7" } });
  const next = { ...revision(), source_sha256: fixturePdfHash, id: "new-revision" };
  next.sections[0].blocks[0] = { ...next.sections[0].blocks[0], kind: "formula",
    asset_id: formula.id, text: "Compare $x=y$ in the guide." };
  state.value = { ...state.value, revision: next };
  view.rerender(<Harness withIR onNavigate={navigate} sourceHash={fixturePdfHash} getPdfData={getFixturePdf} />);
  await act(async () => finishOld({
    source_current: true, evidence: { ...formula, id: "old-formula", locator: { kind: "page", page: 9 } },
  }));
  expect(view.container.querySelector(".paper-guide-block .katex")).not.toBeNull();
  expect(screen.queryByRole("button", { name: "Open PDF page 10" })).not.toBeInTheDocument();
  await screen.findByText("Preview unavailable. Open the PDF to inspect the original.");
  fireEvent.click(await screen.findByRole("button", { name: "Open PDF page 4" }));
  await waitFor(() => expect(navigate).toHaveBeenCalledWith(expect.objectContaining({
    id: "new-formula", locator: { kind: "page", page: 3 },
  })));
});

it("does not offer direct original navigation when file bytes disagree with matching guide and IR metadata", async () => {
  const original = { ...revision(), source_sha256: fixturePdfHash };
  original.sections[0].blocks[0].asset_id = "original";
  state.value = { ...state.value, revision: original };
  api.evidence.mockResolvedValueOnce({ source_current: true,
    evidence: { id: "original", source_block_id: "f", source_type: "image", text: "",
      locator: { kind: "page", page: 2 } },
    original_display: { kind: "figure", page: 2, bbox: [.1,.2,.3,.4] },
  });
  const navigate = vi.fn();
  const wrongVersion = {};
  render(<Harness withIR onNavigate={navigate} sourceHash={fixturePdfHash} getPdfData={getDifferentPdf}
    getPdfVersion={() => wrongVersion} />);
  openGuide("Guide");
  await screen.findByText("Preview unavailable. Open the PDF to inspect the original.");
  expect(screen.queryByRole("button", { name: "Open PDF page 3" })).not.toBeInTheDocument();
  expect(navigate).not.toHaveBeenCalled();
});

it("checks the FileViewer version again before using a previously verified original page button", async () => {
  const original = { ...revision(), source_sha256: fixturePdfHash };
  original.sections[0].blocks[0].asset_id = "original";
  state.value = { ...state.value, revision: original };
  api.evidence.mockResolvedValueOnce({ source_current: true,
    evidence: { id: "original", source_block_id: "f", source_type: "image", text: "",
      locator: { kind: "page", page: 2 } },
    original_display: { kind: "figure", page: 2, bbox: [.1,.2,.3,.4] },
  });
  const navigate = vi.fn();
  let bytes = fixturePdfData;
  render(<Harness withIR onNavigate={navigate} sourceHash={fixturePdfHash} getPdfData={() => bytes.slice(0)}
    getPdfVersion={() => bytes} />);
  openGuide("Guide");
  const open = await screen.findByRole("button", { name: "Open PDF page 3" });
  bytes = getDifferentPdf();
  fireEvent.click(open);
  await waitFor(() => expect(open).not.toBeInTheDocument());
  expect(navigate).not.toHaveBeenCalled();
});

it("places the mode switch in the PDF controls row instead of a second toolbar", () => {
  const view = render(<Harness inlineSwitch />);
  expect(screen.getByRole("navigation", { name: "PDF tools" })).toContainElement(screen.getByRole("group", { name: "Reading mode" }));
  expect(view.container.querySelector(".paper-guide-toolbar")).toBeNull();
  openGuide("Guide");
  expect(screen.getAllByRole("group", { name: "Reading mode" })).toHaveLength(1);
  expect(screen.queryByRole("button", { name: "Zoom" })).toBeNull();
});
it("immediately acknowledges generation while the request is still pending", async () => {
  state.value = { ...state.value, revision: undefined, status: { ...status(), published_revision: null, candidate_revision: null } };
  let reject!: (error: Error) => void;
  api.generate.mockImplementationOnce(() => new Promise((_, fail) => { reject = fail; }));
  render(<Harness />);
  openGuide("Guide");
  fireEvent.click(screen.getByRole("button", { name: "Prepare paper and generate guide" }));
  expect(screen.getByText("Starting")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Generation status and actions" }));
  expect(screen.getByText(/generation usually takes about 2 minutes after it starts/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Submitting…" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Prepare paper and generate guide" })).toBeNull();
  await act(async () => reject(new Error("Connection lost")));
  expect(screen.getByRole("alert")).toHaveTextContent("Connection lost");
  expect(screen.getByRole("button", { name: "Prepare paper and generate guide" })).toBeEnabled();
});

it("shows the acknowledged queue before refresh finishes and times the first readable draft", async () => {
  state.value = { ...state.value, revision: undefined, status: { ...status(), published_revision: null, candidate_revision: null } };
  const draft = { ...revision(), run: { ...revision().run, stage: "writing" } };
  api.generate.mockResolvedValueOnce({ id: draft.id, run: { stage: "queued" } });
  const view = render(<Harness />);
  openGuide("Guide");
  fireEvent.click(screen.getByRole("button", { name: "Prepare paper and generate guide" }));
  expect(await screen.findByText("Queued")).toBeVisible();
  const plan = { layout: "flashpapers", sections: [{ role: "overview", title: "Paper Summary" }] };
  state.value = { ...state.value, revision: { ...draft, plan, sections: [] },
    status: { ...status(), published_revision: null, candidate_revision: draft } };
  view.rerender(<Harness />);
  const timing = (key: string) => JSON.parse(localStorage.getItem("lazymind.paper-guide.click-to-" + key) || "null");
  expect(timing("outline").samples).toHaveLength(1);
  expect(timing("firstParagraph")).toBeNull();
  state.value.revision = { ...draft, plan, sections: [{ ...draft.sections[0], role: "overview" }] };
  view.rerender(<Harness />);
  expect(timing("firstParagraph").p90).toBeGreaterThanOrEqual(timing("outline").p50);
  view.rerender(<Harness />);
  expect(timing("firstParagraph").samples).toHaveLength(1);
});

it("regenerates a completed guide with the selected model while retaining the current article", async () => {
  render(<ConfigProvider theme={{ token: { motion: false } }}><Harness /></ConfigProvider>);
  openGuide("Guide");
  fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
  await waitFor(() => expect(screen.getByRole("dialog")).toBeVisible());
  await chooseOption("Model for this run", "Per-run model");
  fireEvent.click(screen.getByRole("button", { name: "Start regeneration" }));
  await waitFor(() => expect(api.generate).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({model_ref: "own:run", regenerate: true, expected_source_hash: "hash"})));
  expect(screen.getByRole("article")).toBeVisible();
});
it("does not submit regeneration when the model dialog is canceled", () => {
  render(<ConfigProvider theme={{ token: { motion: false } }}><Harness /></ConfigProvider>);
  openGuide("Guide");
  fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(api.generate).not.toHaveBeenCalled();
});

it("discards a canceled model choice before reopening regeneration", async () => {
  render(<Harness />);
  openGuide();
  const open = () => fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
  open();
  await chooseOption("Model for this run", "Per-run model");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  open();
  fireEvent.click(screen.getByRole("button", { name: "Start regeneration" }));
  await waitFor(() => expect(api.generate).toHaveBeenCalledTimes(1));
  expect(api.generate.mock.calls[0][1]).not.toHaveProperty("model_ref");
});

it("keeps the second paper's pending feedback when the first paper's request fails late", async () => {
  state.value = { ...state.value, revision: undefined, status: { ...status(), published_revision: null, candidate_revision: null } };
  let rejectFirst!: (error: Error) => void;
  api.generate.mockImplementationOnce(() => new Promise((_, reject) => { rejectFirst = reject; }));
  api.generate.mockImplementationOnce(() => new Promise(() => {}));
  const view = render(<Harness />);
  const start = () => {
    openGuide();
    fireEvent.click(screen.getByRole("button", { name: "Prepare paper and generate guide" }));
  };
  start();
  view.rerender(<Harness document="second" />);
  start();
  await act(async () => rejectFirst(new Error("First paper failed")));
  expect(screen.getByText("Starting")).toBeVisible();
  expect(screen.queryByText("First paper failed")).toBeNull();
});

it("keeps generation UI in the toolbar and out of the reading pane", async () => {
  const article = revision();
  state.value = { ...state.value, revision: article, status: { ...status(), candidate_revision: { ...article, run: { ...article.run, stage: "writing" } } } };
  const view = render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Guide" }));
  const trigger = screen.getByRole("button", { name: "Generation status and actions" });
  expect(trigger.closest(".paper-guide-toolbar")).not.toBeNull();
  expect(view.container.querySelector(".paper-guide-main .guide-generation-progress")).toBeNull();
  expect(view.container.querySelector(".paper-guide-main .paper-guide-status")).toBeNull();
  expect(screen.queryByRole("button", { name: "Cancel remaining generation" })).toBeNull();
  fireEvent.click(trigger);
  fireEvent.click(await screen.findByRole("button", { name: "Cancel remaining generation" }));
  await waitFor(() => expect(api.cancel).toHaveBeenCalled());
});

it("generates in a selected language without changing the application locale", async () => {
  state.value = { ...state.value, revision: undefined, status: { ...status(), published_revision: null, candidate_revision: null } };
  api.generate.mockResolvedValueOnce({});
  render(<Harness />);
  openGuide();
  await chooseOption("Guide language", "简体中文");
  fireEvent.click(screen.getByRole("button", { name: "Prepare paper and generate guide" }));
  await waitFor(() => expect(api.generate).toHaveBeenCalledWith(
    { dataset: "ds", document: "doc", language: "zh" }, expect.objectContaining({ language: "zh", regenerate: false })));
  expect(locale.language).toBe("en");
  await waitFor(() => expect(state.read).toHaveBeenLastCalledWith("ds", "doc", "zh", true));
});

it("lets regeneration select another language and keeps cancel from applying it", async () => {
  state.value = { ...state.value, revision: { ...revision(), run: { ...revision().run, stage: "completed" } }, status: { ...status(), candidate_revision: null } };
  api.generate.mockResolvedValueOnce({});
  render(<Harness />);
  openGuide();
  fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
  await chooseOption("Guide language", "简体中文");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(api.generate).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
  expect(screen.getByRole("combobox", { name: "Guide language" }).closest(".ant-select")).toHaveTextContent("English");
  await chooseOption("Guide language", "简体中文");
  fireEvent.click(screen.getByRole("button", { name: "Start regeneration" }));
  await waitFor(() => expect(api.generate).toHaveBeenCalledWith(
    { dataset: "ds", document: "doc", language: "zh" }, expect.objectContaining({ language: "zh", regenerate: true })));
});


it("switches reading language without generating and scopes the choice to the document", async () => {
  const view = render(<Harness />);
  openGuide();
  await chooseOption("Reading language", "简体中文");
  expect(state.read).toHaveBeenLastCalledWith("ds", "doc", "zh", true);
  expect(api.generate).not.toHaveBeenCalled();
  state.value = { ...state.value, revision: undefined, status: { ...status(), published_revision: null, candidate_revision: null } };
  view.rerender(<Harness />);
  expect(screen.getByRole("combobox", { name: "Reading language" }).closest(".ant-select")).toHaveTextContent("简体中文");
  await chooseOption("Reading language", "English");
  expect(state.read).toHaveBeenLastCalledWith("ds", "doc", "en", true);
  await chooseOption("Reading language", "简体中文");
  view.rerender(<Harness document="other" />);
  expect(state.read).toHaveBeenLastCalledWith("ds", "other", "en", true);
  view.rerender(<Harness document="doc" />);
  expect(state.read).toHaveBeenLastCalledWith("ds", "doc", "zh", true);
});

it("remembers paper color independently of lighting across papers", async () => {
  const view = render(<Harness />);
  openGuide();
  fireEvent.click(screen.getByRole("button", { name: "Page color" }));
  fireEvent.click(await screen.findByText("Warm paper"));
  fireEvent.click(screen.getByRole("button", { name: "Lights off" }));
  const workspace = view.container.querySelector(".paper-reading-workspace");
  expect(workspace).toHaveAttribute("data-guide-paper", "cream");
  expect(workspace).toHaveAttribute("data-guide-light", "night");
  fireEvent.click(screen.getByRole("button", { name: "Original" }));
  expect(workspace).not.toHaveAttribute("data-guide-paper");
  view.unmount();
  const next = render(<Harness document="another-paper" />);
  openGuide();
  fireEvent.click(screen.getByRole("button", { name: "Lights on" }));
  expect(next.container.querySelector(".paper-reading-workspace")).toHaveAttribute("data-guide-paper", "cream");
});
