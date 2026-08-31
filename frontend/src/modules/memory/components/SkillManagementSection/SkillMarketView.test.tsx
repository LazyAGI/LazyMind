import { render, screen } from "@testing-library/react";
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
});
