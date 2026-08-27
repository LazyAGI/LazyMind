import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import DetailPage from "./DetailPage";
import { getShowcaseCase, type ShowcaseCase } from "./api";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string, values?: Record<string, string>) => values?.output || key,
  }),
}));
vi.mock("./api", () => ({ getShowcaseCase: vi.fn() }));

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
      html_url: template === "html_preview_v1" ? "/showcase-assets/demo/1.0.0/preview.html" : undefined,
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
    gallery: true,
    image_url: "/showcase/demo.png",
    output_label: "Report",
    output_type: "report",
    prompt: tasks[0].prompt,
    prompt_short: tasks[0].prompt_short,
    result_summary: tasks[0].result.summary,
    tasks,
    type: "chat",
  };
}

function LocationProbe() {
  const location = useLocation();
  return <div>{`${location.pathname}${location.search}`}</div>;
}

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={["/agent/chat/cases/demo"]}>
      <Routes>
        <Route path="/agent/chat/cases/:caseId" element={<DetailPage />} />
        <Route path="/agent/chat/home" element={<LocationProbe />} />
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

  it("launches a workflow detail in the New task entry with its selected demo", async () => {
    const item = showcaseCase([task("single", "Single", "Single result")]);
    item.type = "workflow";
    getShowcaseCaseMock.mockResolvedValue(item);
    renderDetail();

    fireEvent.click(await screen.findByRole("button", { name: "showcase.try" }));

    expect(await screen.findByText(
      "/agent/chat/home?showcase_case=demo&showcase_task=single&showcase_entry=work",
    )).toBeInTheDocument();
  });

  it("uses the mouse wheel to scroll an overflowing task list horizontally", async () => {
    getShowcaseCaseMock.mockResolvedValue(showcaseCase([
      task("first", "First", "First result"),
      task("second", "Second", "Second result"),
    ]));
    renderDetail();

    const taskList = await screen.findByRole("group", { name: "showcase.chooseTask" });
    Object.defineProperties(taskList, {
      clientWidth: { configurable: true, value: 300 },
      scrollWidth: { configurable: true, value: 800 },
      scrollLeft: { configurable: true, value: 0, writable: true },
    });

    const taskSection = taskList.closest(".showcase-detail-tasks");
    expect(taskSection).not.toBeNull();
    fireEvent.wheel(taskSection!, { deltaY: 120 });

    expect(taskList.scrollLeft).toBe(120);
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

  it("renders interactive HTML results in a script-only sandbox", async () => {
    getShowcaseCaseMock.mockResolvedValue(showcaseCase([
      task("html", "HTML", "Interactive result", "html_preview_v1"),
    ]));
    renderDetail();

    const preview = await screen.findByTitle("Interactive result");
    expect(preview).toHaveAttribute("src", "/showcase-assets/demo/1.0.0/preview.html");
    expect(preview).toHaveAttribute("sandbox", "allow-scripts");
    expect(preview.getAttribute("sandbox")).not.toContain("allow-same-origin");
    expect(preview.closest(".showcase-result-body")).toHaveClass("is-html-preview");
  });
});
