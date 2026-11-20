import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import EpisodeMemorySection from ".";
import { listEpisodes, type EpisodeListPage } from "../../episodeApi";

vi.mock("../../episodeApi", () => ({ listEpisodes: vi.fn() }));
vi.mock("@/components/request", () => ({ getLocalizedErrorMessage: () => "Request failed" }));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "en" },
    t: (key: string, args?: { start: number; end: number }) =>
      key === "admin.memoryPageRange" ? `${args?.start}–${args?.end}` : key,
  }),
}));
afterEach(cleanup);

function episodePage(start: number): EpisodeListPage {
  return {
    totalSize: 10,
    supportsPageNumber: true,
    nextPageToken: "",
    items: Array.from({ length: 5 }, (_, index) => ({
      id: String(start + index), summary: `Episode ${start + index}`,
      episodeType: "event", hitCount: 0, recordedAtMs: 0, occurredAtMs: 0,
      sourceKind: "", conversationId: "",
    })),
  };
}

it("keeps the displayed rows and pager in place until the requested page arrives", async () => {
  let finishPage!: (page: EpisodeListPage) => void;
  vi.mocked(listEpisodes).mockResolvedValueOnce(episodePage(1)).mockReturnValueOnce(
    new Promise<EpisodeListPage>(resolve => { finishPage = resolve; }),
  );
  render(<EpisodeMemorySection />);
  const firstRow = await screen.findByText("Episode 1");
  const pager = screen.getByRole("navigation");
  fireEvent.click(within(pager).getByText("2"));

  expect(listEpisodes).toHaveBeenLastCalledWith({ page: 2, pageSize: 5 });
  expect(firstRow).toBeVisible();
  expect(within(pager).getByText("1–5")).toBeVisible();
  await act(async () => finishPage(episodePage(6)));
  expect(screen.getByText("Episode 6")).toBeVisible();
  expect(firstRow).not.toBeInTheDocument();
  expect(screen.getByRole("navigation")).toBe(pager);
  expect(within(pager).getByText("6–10")).toBeVisible();
});
