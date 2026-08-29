import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
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

  it("shows HOT only for Workflow cards in the Featured section", () => {
    const { rerender } = render(
      <MemoryRouter>
        <CaseCard item={{ ...item, type: "workflow" }} showWorkflowHot />
      </MemoryRouter>,
    );

    const hotBadge = screen.getByRole("img", { name: "HOT" });
    expect(hotBadge).toBeInTheDocument();
    expect(hotBadge).toHaveClass("showcase-workflow-hot");
    expect(hotBadge.parentElement).toHaveClass("showcase-card-image-stage");
    expect(screen.getByRole("link", { name: /产品设计与 PRD 生成/ })).not.toContainElement(
      hotBadge,
    );

    rerender(
      <MemoryRouter>
        <CaseCard item={{ ...item, type: "work" }} showWorkflowHot />
      </MemoryRouter>,
    );
    expect(screen.queryByRole("img", { name: "HOT" })).not.toBeInTheDocument();

    rerender(
      <MemoryRouter>
        <CaseCard item={{ ...item, type: "workflow" }} />
      </MemoryRouter>,
    );
    expect(screen.queryByRole("img", { name: "HOT" })).not.toBeInTheDocument();
  });
});
