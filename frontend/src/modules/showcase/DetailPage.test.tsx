import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { screen, waitFor } from "@/test/testUtils";
import DetailPage from "./DetailPage";
import { getShowcaseCase, type ShowcaseCase } from "./api";
import { installShowcaseTestTranslations } from "./testTranslations";

vi.mock("./api", () => ({
  getShowcaseCase: vi.fn(),
}));

const mockGetShowcaseCase = vi.mocked(getShowcaseCase);

const item: ShowcaseCase = {
  id: "knowledgeQa",
  title: "知识库问答",
  description: "从资料中快速找到可靠答案。",
  category: "办公效率",
  output_type: "answer",
  output_label: "知识问答",
  image_url: "/showcase/16-knowledge-qa.png",
  attachment_hint: "知识库资料",
  prompt_short: "回答知识库问题",
  prompt: "请根据资料回答我的问题",
  result_summary: "回答并标注依据",
  result_highlights: ["结论", "依据"],
  steps: [
    { title: "检索资料", description: "定位相关内容" },
    { title: "组织答案", description: "输出清晰结论" },
  ],
};

describe("Showcase DetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    installShowcaseTestTranslations();
    mockGetShowcaseCase.mockResolvedValue(item);
  });

  it("renders the replay workspace and a real-chat entry action", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/cases/knowledgeQa"]}>
        <Routes>
          <Route path="/agent/chat/cases/:caseId" element={<DetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect((await screen.findAllByText("知识库问答")).length).toBeGreaterThan(0);
    expect(screen.getByText("任务执行回放")).toBeInTheDocument();
    expect(screen.getByText("最终产出")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "试一试" })).toBeInTheDocument();
    await waitFor(() => expect(mockGetShowcaseCase).toHaveBeenCalledWith("knowledgeQa", expect.anything()));
  });

  it("renders configured task cards for cases with multiple entry tasks", async () => {
    mockGetShowcaseCase.mockResolvedValue({
      ...item,
      id: "aiProduct",
      title: "产品设计与 PRD 生成",
      tasks: [
        {
          id: "product-design",
          title: "产品设计",
          description: "梳理产品结构和任务流程",
          output_label: "产品设计方案",
          prompt: "完成产品设计",
        },
        {
          id: "prd-generation",
          title: "PRD 生成",
          description: "输出可评审的需求文档",
          output_label: "产品需求文档",
          prompt: "生成完整 PRD",
        },
      ],
    });

    render(
      <MemoryRouter initialEntries={["/agent/chat/cases/aiProduct"]}>
        <Routes>
          <Route path="/agent/chat/cases/:caseId" element={<DetailPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText("选择你想完成的产品任务")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /PRD 生成/ })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("正在生成最终成果");

    fireEvent.click(screen.getByRole("button", { name: "直接看结果" }));

    expect(screen.getByText("面向知识工作者的任务执行型 AI Agent")).toBeInTheDocument();
    expect(screen.getByText("核心使用路径")).toBeInTheDocument();
  });
});
