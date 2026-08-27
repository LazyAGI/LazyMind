import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DetailPage from "./DetailPage";
import { getShowcaseCase, type ShowcaseCase } from "./api";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string, values?: Record<string, string>) => values?.output || key,
  }),
}));
vi.mock("./api", () => ({
  getShowcaseCase: vi.fn(),
}));

const getShowcaseCaseMock = vi.mocked(getShowcaseCase);

function task(id: string, title: string, resultTitle: string, template = "generic_report_v1") {
  return {
    id,
    title,
    description: `${title} description`,
    output_label: `${title} output`,
    prompt: `${title} prompt`,
    prompt_short: `${title} user task`,
    steps: [{ title: `${title} flow`, description: `${title} flow description` }],
    result: {
      template,
      eyebrow: `${title} eyebrow`,
      title: resultTitle,
      summary: `${title} summary`,
      highlights: [`${title} highlight`],
      product_report: template === "product_report_v1" ? {
        metrics: [{ label: "Configured metric", value: "42", hint: "Configured hint" }],
        sections: [{
          title: "Configured section",
          marker: "number",
          items: [{ label: "Configured label", description: "Configured description" }],
        }],
        deliverables: "Configured deliverables",
      } : undefined,
    },
  };
}

function showcaseCase(tasks: ReturnType<typeof task>[]): ShowcaseCase {
  return {
    id: "demo",
    source_url: "https://skillhub.example/demo",
    title: "Card title",
    description: "Card description",
    detail_title: "Configured detail title",
    detail_description: "Configured detail description",
    category: "Demo",
    featured: true,
    featured_order: 1,
    gallery: true,
    image_url: "/showcase/demo.png",
    output_label: "Report",
    output_type: "report",
    provider: "SkillHub",
    result_summary: tasks[0].result.summary,
    tasks,
    type: "chat",
  };
}

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={["/agent/chat/cases/demo"]}>
      <Routes>
        <Route path="/agent/chat/cases/:caseId" element={<DetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Showcase DetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn(() => ({ matches: true })),
    });
  });

  it("renders a single-task experience without a task selector", async () => {
    getShowcaseCaseMock.mockResolvedValue(showcaseCase([task("single", "Single", "Single result")]));
    renderDetail();

    expect(await screen.findByText("Configured detail title")).toBeInTheDocument();
    const sourceLink = screen.getByRole("link", { name: "Configured detail title" });
    expect(sourceLink).toHaveAttribute("href", "https://skillhub.example/demo");
    expect(sourceLink).toHaveAttribute("target", "_blank");
    expect(sourceLink).toHaveAttribute("rel", "noreferrer");
    expect(screen.queryByText("showcase.chooseTask")).not.toBeInTheDocument();
    expect(await screen.findByText("Single result")).toBeInTheDocument();
  });

  it("does not render an internal Skill source as a link", async () => {
    const item = showcaseCase([task("single", "Single", "Single result")]);
    item.source_url = "builtin://featured/market-researcher/skill";
    getShowcaseCaseMock.mockResolvedValue(item);
    renderDetail();

    expect(await screen.findByRole("heading", { name: "Configured detail title" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Configured detail title" })).not.toBeInTheDocument();
  });

  it("switches replay and result content for a multi-task experience", async () => {
    getShowcaseCaseMock.mockResolvedValue(showcaseCase([
      task("first", "First", "First result"),
      task("second", "Second", "Second result"),
    ]));
    renderDetail();

    fireEvent.click(await screen.findByRole("button", { name: /Second/ }));
    await waitFor(() => expect(screen.getAllByText("Second flow")).toHaveLength(2));
    expect(await screen.findByText("Second result")).toBeInTheDocument();
  });

  it("renders product report text entirely from configured slots", async () => {
    getShowcaseCaseMock.mockResolvedValue(showcaseCase([
      task("product", "Product", "Configured product result", "product_report_v1"),
    ]));
    renderDetail();

    expect(await screen.findByText("Configured product result")).toBeInTheDocument();
    expect(screen.getByText("Configured metric")).toBeInTheDocument();
    expect(screen.getByText("Configured section")).toBeInTheDocument();
    expect(screen.getByText("Configured deliverables")).toBeInTheDocument();
  });
});
