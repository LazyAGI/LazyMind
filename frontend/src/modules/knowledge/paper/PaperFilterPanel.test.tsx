import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import PaperFilterPanel from "./PaperFilterPanel";
import { DEFAULT_PAPER_FILTERS, type PaperFilters } from "./paperFilters";
import type { PaperItem } from "./model";

const papers: PaperItem[] = [{ id: "paper", title: "Paper", authors: "Author", venue: "Journal", year: 2024, citationCount: 30, folderId: null, status: "unread", important: false, source: "doi", tags: [], parse: "ready" }];
function Harness({ initial = DEFAULT_PAPER_FILTERS }: { initial?: PaperFilters }) {
  const [value, setValue] = useState(initial);
  return <><PaperFilterPanel papers={papers} value={value} onChange={setValue} /><output aria-label="筛选条件">{JSON.stringify(value)}</output></>;
}

describe("PaperFilterPanel", () => {
  it("changes one filter without discarding other selected groups", () => {
    render(<Harness />);
    fireEvent.click(within(screen.getByRole("group", { name: "星标" })).getByRole("button", { name: "已星标" }));
    fireEvent.click(within(screen.getByRole("group", { name: "阅读状态" })).getByRole("button", { name: "阅读中" }));
    const value = JSON.parse(screen.getByLabelText("筛选条件").textContent || "{}");
    expect(value.star).toBe("starred");
    expect(value.status).toBe("reading");
    expect(within(screen.getByRole("group", { name: "星标" })).getByRole("button", { name: "已星标" })).toHaveAttribute("aria-pressed", "true");
  });

  it("clears all groups and ranges", () => {
    render(<Harness initial={{ ...DEFAULT_PAPER_FILTERS, star: "starred", pdf: "uploaded", folder: "filed", status: "read", yearRange: [2024, 2024], minCitations: 20 }} />);
    fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
    expect(JSON.parse(screen.getByLabelText("筛选条件").textContent || "{}")).toEqual(DEFAULT_PAPER_FILTERS);
  });

  it("exposes minimum citations as an editable numeric field", () => {
    render(<Harness />);
    fireEvent.change(screen.getByRole("spinbutton", { name: "最低引用量" }), { target: { value: "12" } });
    expect(JSON.parse(screen.getByLabelText("筛选条件").textContent || "{}").minCitations).toBe(12);
  });

  it("handles an empty library without fabricated years", () => {
    render(<PaperFilterPanel papers={[]} value={DEFAULT_PAPER_FILTERS} onChange={() => {}} />);
    expect(screen.getByText("暂无年份数据")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "清除筛选" })).toBeDisabled();
  });
});
