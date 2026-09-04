import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import CaseCard from "./CaseCard";
import type { ShowcaseCase } from "./api";

vi.mock("react-i18next", () => ({
  useTranslation: () => {
    const labels: Record<string, string> = {
      "showcase.viewDetail": "查看详情",
      "showcase.try": "试一试",
      "showcase.experienceNow": "立即体验",
      "showcase.cardTagsLabel": "能力标签",
      "showcase.filters.capability.chat": "聊天",
      "showcase.filters.capability.work": "任务",
      "showcase.filters.technology.skill": "技能",
      "showcase.filters.technology.workflow": "工作流",
      "showcase.workflowHotBadge": "HOT",
    };
    return {
      t: (key: string, values?: { title?: string }) =>
        key === "showcase.resultPreviewAlt"
          ? `${values?.title ?? ""} result preview`
          : labels[key] || key,
    };
  },
}));

const item: ShowcaseCase = {
  builtin_skill_uid: "builtin.product-design",
  id: "aiProduct",
  category: "product",
  description: "从需求生成产品方案",
  detail_description: "产品设计详情",
  detail_title: "产品设计",
  featured: true,
  featured_order: 1,
  gallery: true,
  hot: true,
  image_url: "/showcase/product.png",
  output_label: "PRD",
  output_type: "document",
  provider: "SkillHub",
  result_summary: "产品需求文档",
  source_url: "https://skillhub.example/aiProduct",
  tasks: [{
    id: "product-plan",
    title: "产品方案",
    description: "生成产品方案",
    output_label: "PRD",
    prompt: "帮我生成一份产品方案",
    prompt_short: "生成产品方案",
    steps: [],
    result: {
      template: "generic_report_v1",
      eyebrow: "产品方案",
      title: "产品方案",
      summary: "产品需求文档",
    },
  }],
  title: "产品设计与 PRD 生成",
  type: "chat",
};

function LocationStateProbe() {
  const location = useLocation();
  const state = location.state as { showcaseReturnTo?: string } | null;
  return <output>{state?.showcaseReturnTo || "no-return-route"}</output>;
}

describe("CaseCard", () => {
  it("uses the card body for try and keeps the corner action for details", () => {
    const onTry = vi.fn();
    render(
      <MemoryRouter>
        <CaseCard item={item} onTry={onTry} />
      </MemoryRouter>,
    );

    expect(screen.getByRole("link", { name: /产品设计与 PRD 生成/ })).toHaveAttribute(
      "href",
      "/agent/chat/home?showcase_case=aiProduct&showcase_entry=chat",
    );
    fireEvent.click(screen.getByRole("link", { name: /产品设计与 PRD 生成/ }));
    expect(onTry).toHaveBeenCalledOnce();
    expect(onTry).toHaveBeenCalledWith(item);
    expect(screen.queryByText("SkillHub")).not.toBeInTheDocument();
    expect(screen.getByRole("img", { name: "聊天" })).toBeInTheDocument();
    expect(screen.getByLabelText("能力标签")).toHaveTextContent("product聊天技能");

    expect(screen.getByRole("link", { name: /查看详情/ })).toHaveAttribute(
      "href",
      "/agent/chat/cases/aiProduct",
    );
    expect(screen.queryByText("试一试")).not.toBeInTheDocument();
  });

  it("routes task capabilities to the New task entry", () => {
    render(
      <MemoryRouter>
        <CaseCard item={{ ...item, id: "ppt-workflow", type: "workflow" }} />
      </MemoryRouter>,
    );

    expect(screen.getByRole("link", { name: /产品设计与 PRD 生成/ })).toHaveAttribute(
      "href",
      "/agent/chat/home?showcase_case=ppt-workflow&showcase_entry=work",
    );
  });

  it("uses details as the primary action in the capability center", () => {
    render(
      <MemoryRouter>
        <CaseCard item={item} primaryAction="details" />
      </MemoryRouter>,
    );

    expect(screen.getByRole("link", { name: "查看详情" })).toHaveAttribute(
      "href",
      "/agent/chat/cases/aiProduct",
    );
    expect(screen.getByRole("link", { name: /产品设计与 PRD 生成/ })).toHaveAttribute(
      "href",
      "/agent/chat/cases/aiProduct",
    );
    expect(screen.getByRole("link", { name: /立即体验/ })).toHaveAttribute(
      "href",
      "/agent/chat/home?showcase_case=aiProduct&showcase_entry=chat",
    );
  });

  it("passes the current capability entry route to the detail page", () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home?section=featured"]}>
        <CaseCard item={item} />
        <LocationStateProbe />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("link", { name: /查看详情/ }));

    expect(screen.getByText("/agent/chat/home?section=featured")).toBeInTheDocument();
  });

  it("uses the backend hot field on home and in the capability center", () => {
    const cases = [
      ["ppt-workflow", "AI PPT", true],
      ["image-workflow", "创意图片与表情包生成", true],
      ["academic_research_pipeline", "学术研究与论文写作", true],
      ["product_solution_delivery", "产品方案交付", true],
      ["bid_tech_proposal_writer", "投标技术方案编写", false],
      ["writer-workflow", "AI Writer 长文写作", false],
    ] as const;

    const { rerender } = render(
      <MemoryRouter>
        {cases.map(([id, title, hot]) => (
          <CaseCard key={id} item={{ ...item, id, title, hot, type: "workflow" }} />
        ))}
      </MemoryRouter>,
    );

    expect(screen.getAllByRole("img", { name: "HOT" })).toHaveLength(4);
    cases.slice(0, 4).forEach(([, title]) => {
      const card = screen.getByText(title).closest("article");
      expect(card).not.toBeNull();
      expect(within(card!).getByRole("img", { name: "HOT" })).toHaveClass("showcase-hot");
    });
    cases.slice(4).forEach(([, title]) => {
      const card = screen.getByText(title).closest("article");
      expect(card).not.toBeNull();
      expect(within(card!).queryByRole("img", { name: "HOT" })).not.toBeInTheDocument();
    });

    rerender(
      <MemoryRouter>
        {cases.map(([id, title, hot]) => (
          <CaseCard
            key={id}
            item={{ ...item, id, title, hot, type: "workflow" }}
            primaryAction="details"
          />
        ))}
      </MemoryRouter>,
    );
    expect(screen.getAllByRole("img", { name: "HOT" })).toHaveLength(4);
  });
});
