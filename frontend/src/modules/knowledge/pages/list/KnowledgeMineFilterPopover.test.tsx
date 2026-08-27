import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeAll, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";

import { ALL_TAGS } from "@/modules/knowledge/constants/common";
import KnowledgeMineFilterPopover from "./KnowledgeMineFilterPopover";
import type {
  KnowledgeMineCloudSource,
  KnowledgeMineSort,
} from "./knowledgeMineFilters";

const labels: Record<string, string> = {
  "knowledge.filterAndSort": "筛选和排序",
  "knowledge.filterAndSortAria": "筛选和排序知识库",
  "knowledge.filterAndSortHint": "按标签、云端来源筛选并排序",
  "knowledge.mineAllTags": "全部标签",
  "knowledge.tags": "标签",
  "knowledge.sortLabel": "排序",
  "knowledge.cloudSourceLabel": "云端来源",
  "knowledge.mineSort.all": "全部",
  "knowledge.mineSort.recent_used": "最近使用",
  "knowledge.mineSort.most_used": "最多使用",
  "knowledge.mineSort.latest_updated": "最新更新",
  "knowledge.mineCloudSource.local": "本地文件/本地目录",
  "knowledge.mineCloudSource.feishu": "飞书",
  "knowledge.mineCloudSource.notion": "Notion",
};

const t = ((key: string, values?: Record<string, unknown>) => {
  if (key === "knowledge.filterAndSortActive") {
    return `已应用 ${values?.count} 个筛选或排序条件`;
  }
  return labels[key] || key;
}) as TFunction;

beforeAll(() => {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
  window.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
});

interface FilterHarnessProps {
  primaryFilter?: "tags" | "cloudSource";
  initialTag?: string;
  initialCloudSource?: KnowledgeMineCloudSource;
}

function FilterHarness({
  primaryFilter = "tags",
  initialTag = ALL_TAGS,
  initialCloudSource = "all",
}: FilterHarnessProps) {
  const [open, setOpen] = useState(false);
  const [tag, setTag] = useState(initialTag);
  const [sort, setSort] = useState<KnowledgeMineSort>("all");
  const [cloudSource, setCloudSource] =
    useState<KnowledgeMineCloudSource>(initialCloudSource);

  return (
    <KnowledgeMineFilterPopover
      t={t}
      tags={["客服", "项目交付"]}
      primaryFilter={primaryFilter}
      open={open}
      selectedTag={tag}
      selectedSort={sort}
      selectedCloudSource={cloudSource}
      onOpenChange={setOpen}
      onTagChange={setTag}
      onSortChange={setSort}
      onCloudSourceChange={setCloudSource}
    />
  );
}

describe("KnowledgeMineFilterPopover", () => {
  it("shows only tags and sorting in tag mode", async () => {
    render(<FilterHarness initialCloudSource="feishu" />);

    const trigger = screen.getByRole("button", { name: "筛选和排序" });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(trigger).toHaveAttribute("title", "按标签、云端来源筛选并排序");

    fireEvent.click(trigger);
    expect(await screen.findByRole("dialog", { name: "筛选和排序知识库" })).toBeInTheDocument();
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("group", { name: "标签" })).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "排序" })).toBeInTheDocument();
    expect(
      screen.queryByRole("group", { name: "云端来源" }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("option", { name: "客服" }));
    expect(screen.getByRole("option", { name: "客服" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    fireEvent.click(screen.getByRole("option", { name: "最近使用" }));
    expect(screen.getByRole("option", { name: "最近使用" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByRole("dialog", { name: "筛选和排序知识库" })).toBeInTheDocument();
    expect(trigger).toHaveAttribute("title", "已应用 2 个筛选或排序条件");
  });

  it("shows only cloud sources and sorting in cloud-source mode", async () => {
    render(
      <FilterHarness primaryFilter="cloudSource" initialTag="客服" />,
    );

    const trigger = screen.getByRole("button", { name: "筛选和排序" });
    expect(trigger).toHaveAttribute("title", "按标签、云端来源筛选并排序");
    fireEvent.click(trigger);
    expect(
      await screen.findByRole("group", { name: "云端来源" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "排序" })).toBeInTheDocument();
    expect(
      screen.queryByRole("group", { name: "标签" }),
    ).not.toBeInTheDocument();

    const feishu = screen.getByRole("option", { name: "飞书" });
    fireEvent.click(feishu);
    expect(screen.getByRole("option", { name: "飞书" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(trigger).toHaveAttribute("title", "已应用 1 个筛选或排序条件");
    fireEvent.click(screen.getByRole("option", { name: "飞书" }));
    expect(screen.getByRole("option", { name: "飞书" })).toHaveAttribute(
      "aria-selected",
      "false",
    );
    expect(trigger).toHaveAttribute("title", "按标签、云端来源筛选并排序");
  });

  it("closes on Escape", async () => {
    render(<FilterHarness />);
    const trigger = screen.getByRole("button", { name: "筛选和排序" });
    fireEvent.click(trigger);
    expect(await screen.findByRole("dialog", { name: "筛选和排序知识库" })).toBeInTheDocument();
    const latest = screen.getByRole("option", { name: "最新更新" });
    latest.focus();
    expect(latest).toHaveFocus();

    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "false"));
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
