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

vi.mock("./api", () => ({
  matchesShowcaseEntryType: (capabilityType: string, entryType: string) =>
    entryType === "chat"
      ? capabilityType === "chat"
      : capabilityType === "work" || capabilityType === "workflow",
}));
vi.mock("./CaseCard", () => ({ default: ({ item }: { item: { title: string } }) => <div>{item.title}</div> }));

const cases = [
  { id: "chat-skill", title: "Chat skill", type: "chat", featured: true },
  { id: "work-skill", title: "Work skill", type: "work", featured: true },
  { id: "workflow", title: "Workflow", type: "workflow", featured: true },
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
      "/agent/chat/cases?type=chat",
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

  it("limits the home preview and leaves the full list behind View More", () => {
    const workCases = Array.from({ length: 9 }, (_, index) => ({
      id: `work-${index + 1}`,
      title: `Work ${index + 1}`,
      type: "work",
      featured: true,
    })) as ShowcaseCase[];

    render(
      <MemoryRouter>
        <FeaturedCases type="work" items={workCases} isLoading={false} />
      </MemoryRouter>,
    );

    expect(screen.getByText("Work 1")).toBeInTheDocument();
    expect(screen.getByText("Work 8")).toBeInTheDocument();
    expect(screen.queryByText("Work 9")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /查看更多/ })).toHaveAttribute(
      "href",
      "/agent/chat/cases?type=work",
    );
  });
});
