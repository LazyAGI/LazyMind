import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { MarketSkillAsset } from "./skillMarketMockData";
import SkillMarketView from "./SkillMarketView";

const skill = (id: string, provider?: string): MarketSkillAsset => ({
  id,
  name: id,
  description: `${id} description`,
  category: "writing",
  tags: [],
  content: "",
  marketSource: "builtin",
  provider,
});

describe("SkillMarketView", () => {
  it("shows configured providers and preserves the builtin fallback", () => {
    render(
      <SkillMarketView
        t={(key) => key === "admin.memorySkillSourceBuiltin" ? "平台内置" : key}
        loading={false}
        skillAssets={[skill("qwen-skill", "千问办公"), skill("legacy-skill")]}
        installedSkills={[]}
        isAdmin={false}
        onInstall={vi.fn()}
        onDetail={vi.fn()}
        onDelete={vi.fn()}
        page={1}
        pageSize={8}
        total={0}
        onPageChange={vi.fn()}
      />,
    );

    expect(screen.getByText("千问办公")).toBeInTheDocument();
    expect(screen.getByText("平台内置")).toBeInTheDocument();
  });
  it("shows the recommendation total and localized card text while preserving source identity", () => {
    const item = { ...skill("canonical-tool"), display_name: "中文工具", display_description: "完整的中文工具简介" };
    render(<SkillMarketView
      t={(key, options) => key === "admin.memorySkillMarketRecommendTitle" ? "推荐技能" : key === "admin.memorySkillMarketRecommendCount" ? `共 ${options?.count} 条` : key}
      loading={false} skillAssets={[item]} installedSkills={[]} isAdmin={false}
      onInstall={vi.fn()} onDetail={vi.fn()} onDelete={vi.fn()} page={1} pageSize={12} total={89} onPageChange={vi.fn()}
    />);
    expect(screen.getByRole("heading", {name: "推荐技能"})).toBeInTheDocument();
    expect(screen.getByText("共 89 条")).toBeInTheDocument();
    expect(screen.getByText("中文工具")).toBeInTheDocument();
    expect(screen.getByText("完整的中文工具简介")).toBeInTheDocument();
    expect(screen.queryByText("canonical-tool")).not.toBeInTheDocument();
    expect(item.name).toBe("canonical-tool");
  });

  it("compensates for the measured scrollbar gutter and updates on resize", () => {
    let gutter = 11;
    const offset = vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockImplementation(function (this: HTMLElement) { return this.classList.contains("memory-skill-market-scroll") ? 500 : 0; });
    const client = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(function (this: HTMLElement) { return this.classList.contains("memory-skill-market-scroll") ? 500 - gutter : 0; });
    try {
      const { container } = render(<SkillMarketView t={(key) => key} loading={false} skillAssets={[skill("tool")]} installedSkills={[]} isAdmin={false} onInstall={vi.fn()} onDetail={vi.fn()} onDelete={vi.fn()} page={1} pageSize={12} total={0} onPageChange={vi.fn()} />);
      const scroll = container.querySelector(".memory-skill-market-scroll");
      expect(scroll).toHaveStyle({ marginInlineEnd: "-11px" });
      gutter = 0;
      act(() => window.dispatchEvent(new Event("resize")));
      expect(scroll).toHaveStyle({ marginInlineEnd: "0px" });
    } finally {
      offset.mockRestore();
      client.mockRestore();
    }
  });

});
