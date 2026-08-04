import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import {
  HistorySessionItem,
  HistorySessionModal,
  HistorySessionTab,
} from "./HistorySessions";
import type { SelfEvolutionHistoryEntry } from "./types";

function makeEntry(overrides: Partial<SelfEvolutionHistoryEntry> = {}): SelfEvolutionHistoryEntry {
  return {
    key: "entry-1",
    sessionId: "session-1",
    title: "Session One",
    updatedAt: "2024-01-01",
    messageCount: 3,
    source: "local",
    ...overrides,
  };
}

describe("HistorySessionItem", () => {
  it("calls onSelect when clicking the select button for a non-current entry", () => {
    const onSelect = vi.fn();
    renderWithProviders(
      <HistorySessionItem entry={makeEntry()} isDeleting={false} onSelect={onSelect} onDelete={vi.fn()} />,
    );
    fireEvent.click(screen.getByText("Session One"));
    expect(onSelect).toHaveBeenCalledWith(makeEntry());
  });

  it("disables the select button and shows the current badge for the current entry", () => {
    renderWithProviders(
      <HistorySessionItem entry={makeEntry({ isCurrent: true })} isDeleting={false} onSelect={vi.fn()} onDelete={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.currentSession")).toBeInTheDocument();
    expect(screen.getByText("Session One").closest("button")).toBeDisabled();
  });

  it("calls onDelete when clicking the delete button", () => {
    const onDelete = vi.fn();
    const entry = makeEntry();
    renderWithProviders(
      <HistorySessionItem entry={entry} isDeleting={false} onSelect={vi.fn()} onDelete={onDelete} />,
    );
    fireEvent.click(screen.getByTitle("selfEvolutionRun.deleteHistoryTitle"));
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onDelete.mock.calls[0][0]).toEqual(entry);
  });

  it("disables both buttons while deleting", () => {
    renderWithProviders(
      <HistorySessionItem entry={makeEntry()} isDeleting onSelect={vi.fn()} onDelete={vi.fn()} />,
    );
    expect(screen.getByText("Session One").closest("button")).toBeDisabled();
    expect(screen.getByTitle("selfEvolutionRun.deleteHistoryTitle")).toBeDisabled();
  });
});

describe("HistorySessionTab", () => {
  it("calls onSelect with the sessionId/threadId/title when clicked", () => {
    const onSelect = vi.fn();
    const entry = makeEntry({ threadId: "thread-1" });
    renderWithProviders(
      <HistorySessionTab entry={entry} isDeleting={false} onSelect={onSelect} onDelete={vi.fn()} />,
    );
    fireEvent.click(screen.getByText("Session One"));
    expect(onSelect).toHaveBeenCalledWith({ sessionId: "session-1", threadId: "thread-1", title: "Session One" });
  });

  it("calls onDelete when clicking the delete button", () => {
    const onDelete = vi.fn();
    renderWithProviders(
      <HistorySessionTab entry={makeEntry()} isDeleting={false} onSelect={vi.fn()} onDelete={onDelete} />,
    );
    fireEvent.click(screen.getByTitle("selfEvolutionRun.deleteHistoryTitle"));
    expect(onDelete).toHaveBeenCalledTimes(1);
  });
});

describe("HistorySessionModal", () => {
  it("renders the history entries when open with data", () => {
    renderWithProviders(
      <HistorySessionModal
        open
        threadHistoryListError=""
        isLoadingThreadHistoryList={false}
        historySessionEntries={[makeEntry()]}
        deletingHistoryKeys={[]}
        onCancel={vi.fn()}
        onRetry={vi.fn()}
        onSelectHistorySession={vi.fn()}
        onDeleteHistorySession={vi.fn()}
      />,
    );
    expect(screen.getByText("Session One")).toBeInTheDocument();
  });

  it("shows the empty state when there are no entries and not loading", () => {
    renderWithProviders(
      <HistorySessionModal
        open
        threadHistoryListError=""
        isLoadingThreadHistoryList={false}
        historySessionEntries={[]}
        deletingHistoryKeys={[]}
        onCancel={vi.fn()}
        onRetry={vi.fn()}
        onSelectHistorySession={vi.fn()}
        onDeleteHistorySession={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.noHistory")).toBeInTheDocument();
  });

  it("shows the loading state when loading and there are no entries yet", () => {
    renderWithProviders(
      <HistorySessionModal
        open
        threadHistoryListError=""
        isLoadingThreadHistoryList
        historySessionEntries={[]}
        deletingHistoryKeys={[]}
        onCancel={vi.fn()}
        onRetry={vi.fn()}
        onSelectHistorySession={vi.fn()}
        onDeleteHistorySession={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.loadingHistory")).toBeInTheDocument();
  });

  it("renders an error alert and calls onRetry when the retry button is clicked", () => {
    const onRetry = vi.fn();
    renderWithProviders(
      <HistorySessionModal
        open
        threadHistoryListError="Failed to load"
        isLoadingThreadHistoryList={false}
        historySessionEntries={[]}
        deletingHistoryKeys={[]}
        onCancel={vi.fn()}
        onRetry={onRetry}
        onSelectHistorySession={vi.fn()}
        onDeleteHistorySession={vi.fn()}
      />,
    );
    expect(screen.getByText("Failed to load")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.retry"));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("prefers onEnterHistorySession over onSelectHistorySession when provided", () => {
    const onEnterHistorySession = vi.fn();
    const onSelectHistorySession = vi.fn();
    renderWithProviders(
      <HistorySessionModal
        open
        threadHistoryListError=""
        isLoadingThreadHistoryList={false}
        historySessionEntries={[makeEntry()]}
        deletingHistoryKeys={[]}
        onCancel={vi.fn()}
        onRetry={vi.fn()}
        onSelectHistorySession={onSelectHistorySession}
        onEnterHistorySession={onEnterHistorySession}
        onDeleteHistorySession={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByText("Session One"));
    expect(onEnterHistorySession).toHaveBeenCalledTimes(1);
    expect(onSelectHistorySession).not.toHaveBeenCalled();
  });
});
