import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import GalleryPage from "./GalleryPage";
import { listShowcaseCases, type ShowcaseCase } from "./api";
import { installShowcaseTestTranslations } from "./testTranslations";

vi.mock("./api", () => ({
  listShowcaseCases: vi.fn(),
}));

const mockListShowcaseCases = vi.mocked(listShowcaseCases);

function makeCase(overrides: Partial<ShowcaseCase> = {}): ShowcaseCase {
  return {
    id: "industry",
    title: "行业趋势研究",
    description: "拆解行业变化并形成研究报告。",
    category: "调研分析",
    output_type: "document",
    output_label: "研究报告",
    image_url: "/showcase/01-pet-market.png",
    prompt_short: "分析行业趋势",
    prompt: "请分析这个行业",
    result_summary: "形成结构化报告",
    result_highlights: ["趋势判断"],
    steps: [{ title: "分析", description: "提取关键信息" }],
    ...overrides,
  };
}

describe("Showcase GalleryPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    installShowcaseTestTranslations();
    mockListShowcaseCases.mockResolvedValue({
      cases: [
        makeCase(),
        makeCase({
          id: "sales",
          title: "销售数据分析",
          category: "数据分析",
          prompt_short: "分析销售数据",
        }),
        makeCase({
          id: "paper",
          title: "学术论文写作",
          category: "文档写作",
          prompt_short: "撰写论文",
        }),
      ],
      categories: ["全部", "调研分析", "数据分析", "文档写作"],
      total: 3,
    });
  });

  it("filters cases by category and keyword", async () => {
    renderWithProviders(<GalleryPage />);

    expect(screen.getByRole("link", { name: "返回主页面" })).toHaveAttribute(
      "href",
      "/agent/chat/home",
    );
    expect(screen.getByRole("heading", { name: "能力中心" })).toBeInTheDocument();
    expect(
      screen.getByText("探索 Lazymind 可以帮你完成的任务，从一个好案例开始。"),
    ).toBeInTheDocument();
    expect(await screen.findByText("行业趋势研究")).toBeInTheDocument();
    expect(screen.getByText("销售数据分析")).toBeInTheDocument();
    expect(screen.getAllByRole("heading", { level: 3 }).map((heading) => heading.textContent)).toEqual([
      "学术论文写作",
      "行业趋势研究",
      "销售数据分析",
    ]);
    expect(
      screen
        .getByRole("img", { name: "学术论文写作结果预览" })
        .closest(".showcase-card-image-wrap"),
    ).toHaveClass("showcase-card-cover-document");

    fireEvent.click(screen.getByRole("button", { name: "数据分析" }));
    expect(screen.queryByText("行业趋势研究")).not.toBeInTheDocument();
    expect(screen.getByText("销售数据分析")).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("搜索案例，例如：写作、PPT、图片"), {
      target: { value: "不存在" },
    });
    await waitFor(() => {
      expect(screen.getByText("没有找到相关案例")).toBeInTheDocument();
    });
  });
});
