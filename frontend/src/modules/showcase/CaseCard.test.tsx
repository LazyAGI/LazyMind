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
  prompt: "帮我生成一份产品方案",
  prompt_short: "生成产品方案",
  result_summary: "产品需求文档",
  source_url: "https://skillhub.example/aiProduct",
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
      "/agent/chat/home?showcase_case=aiProduct",
    );
    fireEvent.click(screen.getByRole("link", { name: /产品设计与 PRD 生成/ }));
    expect(onTry).toHaveBeenCalledOnce();
    expect(onTry).toHaveBeenCalledWith(item);

    expect(screen.getByRole("link", { name: /查看详情/ })).toHaveAttribute(
      "href",
      "/agent/chat/cases/aiProduct",
    );
    expect(screen.queryByText("试一试")).not.toBeInTheDocument();
  });
});
