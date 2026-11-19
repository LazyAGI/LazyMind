import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import { expect, it, vi, beforeEach } from "vitest";
import { usePaperGuide, mergeRevision } from "./usePaperGuide";
import { revision, section, status } from "./fixtures";
import PaperGuideReader from "./PaperGuideReader";
const api = vi.hoisted(() => ({
  status: vi.fn(),
  revision: vi.fn(),
  generate: vi.fn(),
  cancel: vi.fn(),
  resume: vi.fn(),
  progress: vi.fn(),
}));
vi.mock("../../api/paperGuide", () => ({ paperGuideApi: api }));
vi.mock("../../api/paperReader", () => ({
  usePaperReaderSession: () => "owner",
}));
beforeEach(() => {
  vi.clearAllMocks();
  api.status.mockResolvedValue({ data: status(), etag: "a" });
  api.revision.mockResolvedValue(revision());
});
it("merges corrected immutable section versions and flags without dropping unchanged content", () => {
  const old = revision();
  const corrected = {
    ...section("contribution"),
    key: "section:contribution:v2",
    version: 2,
    change_version: 7,
  };
  const next = mergeRevision(old, {
    ...old,
    version: 7,
    sections: [
      corrected,
      {
        ...old.sections[0],
        publication_status: "correction_required",
        change_version: 7,
      },
    ],
  });
  expect(next.sections).toHaveLength(4);
  expect(next.sections[2].key).toBe(corrected.key);
  expect(next.sections[0].publication_status).toBe("correction_required");
});
it("keeps the opened published revision when a new published candidate arrives", async () => {
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() => expect(h.result.current.revision?.id).toBe("r1"));
  api.status.mockResolvedValue({
    data: { ...status(), published_revision: { ...revision(), id: "r2" } },
    etag: "b",
  });
  await act(() => h.result.current.refresh());
  expect(h.result.current.revision?.id).toBe("r1");
  expect(h.result.current.availableRevision?.id).toBe("r2");
});
it("aborts and ignores late document responses, then loads with fresh ETag", async () => {
  let resolve!: (v: unknown) => void;
  api.status.mockImplementationOnce(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  const h = renderHook(({ doc }) => usePaperGuide("ds", doc, "zh", true), {
    initialProps: { doc: "one" },
  });
  h.rerender({ doc: "two" });
  await waitFor(() => expect(h.result.current.revision?.id).toBe("r1"));
  await act(async () =>
    resolve({
      data: { ...status(), published_revision: { ...revision(), id: "stale" } },
      etag: "stale",
    }),
  );
  expect(h.result.current.revision?.id).toBe("r1");
  expect(api.status.mock.calls[0][1].signal.aborted).toBe(true);
});
it("lets a slow revision finish across polling ticks", async () => {
  let resolve!: (value: ReturnType<typeof revision>) => void;
  const active = { ...revision(), run: { ...revision().run, stage: "writing" } };
  api.status.mockResolvedValue({ data: { ...status(), candidate_revision: active }, etag: "writing" });
  api.revision.mockImplementationOnce(() => new Promise(r => { resolve = r; }));
  vi.useFakeTimers();
  try {
    const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
    await act(async () => {});
    const signal = api.revision.mock.calls[0][3] as AbortSignal;
    await act(() => vi.advanceTimersByTimeAsync(8000));
    expect(signal.aborted).toBe(false);
    expect(api.status).toHaveBeenCalledTimes(1);
    await act(async () => resolve(revision()));
    expect(h.result.current.revision?.id).toBe("r1");
    await act(() => vi.advanceTimersByTimeAsync(4000));
    expect(api.status).toHaveBeenCalledTimes(2);
    h.unmount();
  } finally {
    vi.useRealTimers();
  }
});
it("checks a first-time draft sooner until the summary is readable", async () => {
  vi.useFakeTimers();
  try {
    const draft = { ...revision(), sections: [], run: { ...revision().run, stage: "writing" } };
    const writing = { ...status(), published_revision: null, candidate_revision: draft };
    api.status.mockResolvedValue({ data: writing });
    api.revision.mockResolvedValueOnce(draft).mockResolvedValue({ ...draft, sections: [section("overview")] });
    const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
    await act(async () => {});
    await act(() => vi.advanceTimersByTimeAsync(1500));
    expect(h.result.current.revision?.sections[0]?.role).toBe("overview");
    await act(() => vi.advanceTimersByTimeAsync(3999));
    expect(api.status).toHaveBeenCalledTimes(2);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(api.status).toHaveBeenCalledTimes(3);
    h.unmount();
  } finally {
    vi.useRealTimers();
  }
});

it("keeps data on 304 and requests incremental corrections", async () => {
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() => expect(h.result.current.revision).toBeDefined());
  api.status.mockResolvedValue({ etag: "a" });
  await act(() => h.result.current.refresh());
  expect(h.result.current.revision?.sections).toHaveLength(4);
  expect(
    api.status.mock.calls[api.status.mock.calls.length - 1]?.[1].etag,
  ).toBe("a");
});
it("removes previously visible content after access is revoked", async () => {
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() => expect(h.result.current.revision).toBeDefined());
  api.status.mockRejectedValue({ response: { status: 404 } });
  await act(() => h.result.current.refresh());
  expect(h.result.current.revision).toBeUndefined();
});
it("does not refetch unchanged revision content on a 304 status", async () => {
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() => expect(h.result.current.revision).toBeDefined());
  api.status.mockResolvedValue({ etag: "a" });
  await act(() => h.result.current.refresh());
  expect(api.revision).toHaveBeenCalledTimes(1);
});
it("uses ordinary polling to refresh source currentness and keeps the same readable sections", async () => {
  const old = revision();
  const summaries = status();
  api.status.mockResolvedValue({ data: summaries, etag: "before-ir-upgrade" });
  api.revision.mockResolvedValue(old);
  function Reader() {
    const { revision } = usePaperGuide("ds", "doc", "en", true);
    return revision && (
      <PaperGuideReader revision={revision} title="Paper" language="en" onEvidence={() => {}} />
    );
  }
  vi.useFakeTimers();
  try {
    const view = render(<Reader />);
    await act(async () => {});
    const article = screen.getByRole("article");
    const body = screen.getAllByText(/A concrete explanation of the argument/)[0];

    api.status
      .mockResolvedValueOnce({ data: summaries, etag: "after-ir-upgrade" })
      .mockResolvedValue({ etag: "after-ir-upgrade" });
    api.revision.mockResolvedValueOnce({ ...old, source_current: false, sections: [] });
    await act(() => vi.advanceTimersByTimeAsync(60000));
    expect(api.status).toHaveBeenLastCalledWith(expect.anything(), expect.objectContaining({ etag: "before-ir-upgrade" }));
    expect(api.revision).toHaveBeenLastCalledWith(expect.anything(), old.id, old.version, expect.anything());
    expect(screen.getByRole("article")).toBe(article);
    expect(screen.getAllByText(/A concrete explanation of the argument/)[0]).toBe(body);

    await act(() => vi.advanceTimersByTimeAsync(60000));
    expect(api.status).toHaveBeenLastCalledWith(expect.anything(), expect.objectContaining({ etag: "after-ir-upgrade" }));
    expect(api.revision).toHaveBeenCalledTimes(2);
    view.unmount();
  } finally {
    vi.useRealTimers();
  }
});
it("polls idle documents once a minute, skips hidden tabs and refreshes on return", async () => {
  vi.useFakeTimers();
  const visibility = vi.spyOn(window.document, "visibilityState", "get").mockReturnValue("hidden");
  try {
    const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
    await act(() => vi.advanceTimersByTimeAsync(60000));
    expect(api.status).not.toHaveBeenCalled();
    visibility.mockReturnValue("visible");
    await act(async () => { window.document.dispatchEvent(new Event("visibilitychange")); });
    expect(api.status).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(59999));
    expect(api.status).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(api.status).toHaveBeenCalledTimes(2);
    visibility.mockReturnValue("hidden");
    await act(() => vi.advanceTimersByTimeAsync(120000));
    expect(api.status).toHaveBeenCalledTimes(2);
    visibility.mockReturnValue("visible");
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    expect(api.status).toHaveBeenCalledTimes(3);
    const refreshAfterMutation = h.result.current.refresh;
    h.unmount();
    await act(() => refreshAfterMutation());
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    expect(api.status).toHaveBeenCalledTimes(3);
  } finally {
    visibility.mockRestore();
    vi.useRealTimers();
  }
});

it("retries a failed revision behind a cached status ETag, keeping its error until corrections arrive", async () => {
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() => expect(h.result.current.revision?.version).toBe(1));
  const failure = new Error("revision temporarily unavailable");
  api.status
    .mockResolvedValueOnce({
      data: {
        ...status(),
        version: 2,
        published_revision: { ...revision(), version: 2 },
      },
      etag: "v2",
    })
    .mockResolvedValue({ etag: "v2" });
  api.revision.mockRejectedValueOnce(failure).mockRejectedValueOnce(failure);
  await act(() => h.result.current.refresh());
  expect(h.result.current.error).toBe(failure);
  await act(() => h.result.current.refresh());
  expect(h.result.current.error).toBe(failure);
  expect(api.revision).toHaveBeenCalledTimes(3);
  const corrected = {
    ...revision(),
    version: 2,
    sections: [
      {
        ...revision().sections[0],
        change_version: 2,
        publication_status: "correction_required",
      },
    ],
  };
  api.revision.mockResolvedValueOnce(corrected);
  await act(() => h.result.current.refresh());
  expect(h.result.current.revision?.sections[0].publication_status).toBe(
    "correction_required",
  );
  expect(h.result.current.revision?.sections).toHaveLength(4);
  expect(h.result.current.error).toBeUndefined();
  await act(() => h.result.current.refresh());
  expect(api.revision).toHaveBeenCalledTimes(4);
});

it.each([false, true])(
  "follows a new candidate from an empty unpublished run (newly completed: %s)",
  async (completed) => {
    const empty = {
      ...revision(),
      sections: [],
      run: { ...revision().run, stage: "paused" },
    };
    const next = { ...revision(), id: "r2" };
    api.status.mockResolvedValue({
      data: {
        ...status(),
        published_revision: null,
        candidate_revision: empty,
      },
      etag: "empty",
    });
    api.revision.mockImplementation(async (_, id) =>
      id === "r2" ? next : empty,
    );
    const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
    await waitFor(() =>
      expect(h.result.current.revision?.sections).toEqual([]),
    );
    api.status.mockResolvedValue({
      data: {
        ...status(),
        published_revision: completed ? next : null,
        candidate_revision: completed ? null : next,
      },
      etag: "new",
    });
    await act(() => h.result.current.refresh());
    expect(h.result.current.revision?.id).toBe("r2");
    expect(h.result.current.revision?.sections).toHaveLength(4);
    expect(api.revision).toHaveBeenLastCalledWith(
      expect.anything(),
      "r2",
      0,
      expect.anything(),
    );
  },
);
it("preserves an unfinished candidate with readable passages when another candidate starts", async () => {
  const partial = {
    ...revision(),
    sections: [section("background")],
    run: { ...revision().run, stage: "paused" },
  };
  api.status.mockResolvedValue({
    data: {
      ...status(),
      published_revision: null,
      candidate_revision: partial,
    },
    etag: "partial",
  });
  api.revision.mockResolvedValue(partial);
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() =>
    expect(h.result.current.revision?.sections).toHaveLength(1),
  );
  api.status.mockResolvedValue({
    data: {
      ...status(),
      published_revision: null,
      candidate_revision: { ...revision(), id: "r2" },
    },
    etag: "new",
  });
  await act(() => h.result.current.refresh());
  expect(h.result.current.revision?.id).toBe("r1");
  expect(h.result.current.revision?.sections).toHaveLength(1);
});

it("reads new chapter progress without replacing the opened article", async () => {
  const old = revision();
  const writing = { ...revision(), id: "new", run: { ...old.run, stage: "writing" } };
  api.status.mockResolvedValue({ data: { ...status(), published_revision: old, candidate_revision: writing }, etag: "writing" });
  api.revision.mockImplementation(async (_scope, id) => id === "new" ? writing : old);
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() => expect(h.result.current.writingRevision?.id).toBe("new"));
  expect(h.result.current.revision?.id).toBe(old.id);
  api.status.mockResolvedValue({ etag: "writing" });
  await act(() => h.result.current.refresh());
  expect(h.result.current.writingRevision?.id).toBe("new");
  expect(api.revision).toHaveBeenCalledTimes(2);
  api.status.mockRejectedValueOnce(new TypeError("offline"));
  await act(() => h.result.current.refresh());
  expect(h.result.current.writingRevision?.sections).toEqual(writing.sections);
  expect(h.result.current.revision?.id).toBe(old.id);
  api.status.mockRejectedValueOnce({ response: { status: 403 } });
  await act(() => h.result.current.refresh());
  expect(h.result.current.writingRevision).toBeUndefined();
  expect(h.result.current.revision).toBeUndefined();
});

it("refreshes a visible draft immediately on network reconnect and coalesces concurrent wakeups", async () => {
  const partial = { ...revision(), sections: [section("overview")], run: { ...revision().run, stage: "writing" } };
  api.status.mockResolvedValue({ data: { ...status(), published_revision: null, candidate_revision: partial }, etag: "draft" });
  api.revision.mockResolvedValue(partial);
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() => expect(h.result.current.revision?.sections).toHaveLength(1));
  api.status.mockRejectedValueOnce(new TypeError("offline"));
  await act(() => h.result.current.refresh());
  expect(h.result.current.revision?.sections).toHaveLength(1);
  expect(h.result.current.error).toBeDefined();
  api.status.mockResolvedValue({ data: status(), etag: "finished" });
  api.revision.mockResolvedValue(revision());
  const attempts = api.status.mock.calls.length;
  await act(async () => {
    window.dispatchEvent(new Event("online"));
    window.dispatchEvent(new Event("online"));
  });
  expect(api.status).toHaveBeenCalledTimes(attempts + 1);
  expect(h.result.current.revision?.run.stage).toBe("completed");
  expect(h.result.current.error).toBeUndefined();
  h.unmount();
  window.dispatchEvent(new Event("online"));
  expect(api.status).toHaveBeenCalledTimes(attempts + 1);
});

it("finishes the writing revision after a refresh interrupts its fetch and status returns 304", async () => {
  const published = revision();
  const writing = { ...revision(), id: "draft", run: { ...published.run, stage: "writing" } };
  api.status.mockResolvedValueOnce({
    data: { ...status(), published_revision: published, candidate_revision: writing },
    etag: "both",
  }).mockResolvedValue({ etag: "both" });
  let finishInterrupted!: (value: typeof writing) => void;
  api.revision
    .mockResolvedValueOnce(published)
    .mockImplementationOnce(() => new Promise(resolve => { finishInterrupted = resolve; }))
    .mockImplementation(async (_, id) => id === "draft" ? writing : published);
  const h = renderHook(() => usePaperGuide("ds", "doc", "zh", true));
  await waitFor(() => expect(api.revision).toHaveBeenCalledTimes(2));
  await act(() => h.result.current.refresh());
  expect(api.revision.mock.calls[1][3].aborted).toBe(true);
  await act(async () => finishInterrupted({ ...writing, version: 0 }));
  expect(h.result.current.writingRevision?.version).toBe(writing.version);
  expect(h.result.current.revision?.id).toBe(published.id);
  h.unmount();
});
