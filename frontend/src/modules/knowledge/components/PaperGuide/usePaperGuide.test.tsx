import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi, beforeEach } from "vitest";
import { usePaperGuide, mergeRevision, unicodeRange } from "./usePaperGuide";
import { revision, section, status } from "./fixtures";
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
it("converts UTF16 ranges to Unicode code points", () => {
  expect(unicodeRange("甲😀乙𐐀丙", 1, 6)).toEqual({ start: 1, end: 4 });
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
