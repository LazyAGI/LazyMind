import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import FeaturedCases from "./FeaturedCases";
import type { ShowcaseCase } from "./api";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string) => ({
      "showcase.featuredTitle": "精选能力",
      "showcase.viewMore": "查看更多",
    })[key] || key,
  }),
}));

vi.mock("./CaseCard", () => ({
  default: ({ item }: { item: { title: string } }) => <div>{item.title}</div>,
}));

const cases = [
  { id: "chat-skill", title: "Chat skill", type: "chat", featured: true, featured_order: 1, hot: false },
  { id: "work-skill", title: "Work skill", type: "work", featured: true, featured_order: 2, hot: false },
  { id: "workflow", title: "Workflow", type: "workflow", featured: true, featured_order: 1, hot: true },
] as ShowcaseCase[];

describe("FeaturedCases", () => {
  it("renders only the Featured Skill type selected by the entry point", () => {
    const view = render(
      <MemoryRouter>
        <FeaturedCases type="chat" items={cases} isLoading={false} />
      </MemoryRouter>,
    );

    expect(screen.getByText("Chat skill")).toBeInTheDocument();
    expect(screen.queryByText("Work skill")).not.toBeInTheDocument();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /查看更多/ })).toHaveAttribute(
      "href",
      "/agent/chat/cases?showcase_entry=chat",
    );

    view.rerender(
      <MemoryRouter>
        <FeaturedCases type="work" items={cases} isLoading={false} />
      </MemoryRouter>,
    );

    expect(screen.getByText("Work skill")).toBeInTheDocument();
    expect(screen.getByText("Workflow")).toBeInTheDocument();
    expect(screen.queryByText("Chat skill")).not.toBeInTheDocument();
  });

  it.each(["chat", "work"] as const)("limits the %s home preview to eight ordered capabilities and keeps View More", (type) => {
    const entries = Array.from({ length: 9 }, (_, index) => ({
      id: `${type}-${index + 1}`,
      title: `Capability ${index + 1}`,
      type,
      featured: true,
      featured_order: index + 1,
      hot: false,
    })) as ShowcaseCase[];

    render(
      <MemoryRouter>
        <FeaturedCases type={type} items={[...entries].reverse()} isLoading={false} />
      </MemoryRouter>,
    );

    expect(screen.getAllByText(/^Capability \d+$/).map((card) => card.textContent)).toEqual(
      entries.slice(0, 8).map((item) => item.title),
    );
    expect(screen.queryByText("Capability 9")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /查看更多/ })).toHaveAttribute(
      "href",
      `/agent/chat/cases?showcase_entry=${type}`,
    );
  });

  it("sorts each entry point by the resolved Featured order", () => {
    const workCases = [
      { id: "later", title: "Later", type: "work", featured: true, featured_order: 2, hot: false },
      { id: "first", title: "First", type: "workflow", featured: true, featured_order: 1, hot: true },
    ] as ShowcaseCase[];

    render(
      <MemoryRouter>
        <FeaturedCases type="work" items={workCases} isLoading={false} />
      </MemoryRouter>,
    );

    const cards = screen.getAllByText(/First|Later/);
    expect(cards.map((card) => card.textContent)).toEqual(["First", "Later"]);
  });

  it.each(["chat", "work"] as const)("shows paper reading first in the %s entry", (type) => {
    render(<MemoryRouter><FeaturedCases type={type} items={[
      ...cases,
      { id: "paper-reading", title: "论文阅读", type: "page", featured: true, featured_order: 0 },
    ] as ShowcaseCase[]} isLoading={false} /></MemoryRouter>);
    expect(screen.getByText("论文阅读").parentElement?.firstElementChild).toHaveTextContent("论文阅读");
  });
});
