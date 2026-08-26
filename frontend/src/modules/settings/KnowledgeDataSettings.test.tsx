import { createRef } from "react";
import type { AnchorHTMLAttributes } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import KnowledgeDataSettings from "./KnowledgeDataSettings";

const mocks = vi.hoisted(() => ({
  disableTool: vi.fn(),
  listToolAssetsPage: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock("antd", async () => {
  const actual = await vi.importActual<typeof import("antd")>("antd");
  return {
    ...actual,
    message: { error: vi.fn(), success: vi.fn() },
  };
});

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      if (key === "settingsPage.knowledge.groups.recognition.multimodal.name") return "多模态识别";
      if (key === "settingsPage.knowledge.groups.retrieval.kb.name") return "知识库";
      if (key === "settingsPage.knowledge.groups.retrieval.kb.description") {
        return "知识库广场、知识库中查找文档、统计信息和相关片段，用于基于资料回答问题。";
      }
      if (key === "settingsPage.knowledge.openConfigAria") return `打开${values?.name}配置`;
      return key;
    },
  }),
}));

vi.mock("react-router-dom", () => ({
  Link: ({ to, ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & { to: string }) => (
    <a
      {...props}
      href={to}
      onClick={(event) => {
        event.preventDefault();
        mocks.navigate(to);
      }}
    />
  ),
}));

vi.mock("@/modules/memory/toolApi", () => ({
  disableTool: mocks.disableTool,
  enableTool: vi.fn(),
  listToolAssetsPage: mocks.listToolAssetsPage,
  notifyToolAvailabilityChanged: vi.fn(),
}));

vi.mock("@/modules/modelProvider/pages/ExternalServicesPage", () => ({
  default: () => <div>
    <button type="button">配置 MinerU</button>
    <button type="button">配置 PaddleOCR</button>
  </div>,
}));

describe("KnowledgeDataSettings", () => {
  beforeEach(() => {
    mocks.navigate.mockReset();
    mocks.disableTool.mockReset();
    mocks.disableTool.mockResolvedValue(undefined);
    mocks.listToolAssetsPage.mockReset();
    mocks.listToolAssetsPage.mockResolvedValue({
      records: [{
        id: "multimodal",
        name: "多模态识别",
        description: "从图片中提取文字和内容描述",
        isEnabled: true,
        readonly: false,
      }],
    });
  });

  it("opens multimodal recognition in the Models & Services settings section", async () => {
    render(<KnowledgeDataSettings
      controlsDisabled={false}
      documentParsingEnabled={false}
      documentParsingSaving={false}
      headingRef={createRef<HTMLHeadingElement>()}
      onDocumentParsingChange={vi.fn()}
    />);

    fireEvent.click(await screen.findByRole("link", { name: "打开多模态识别配置" }));

    expect(mocks.navigate).toHaveBeenCalledWith("/settings?section=models");
  });

  it("keeps the nested switch independent from row navigation", async () => {
    render(<KnowledgeDataSettings
      controlsDisabled={false}
      documentParsingEnabled={false}
      documentParsingSaving={false}
      headingRef={createRef<HTMLHeadingElement>()}
      onDocumentParsingChange={vi.fn()}
    />);

    const switches = await screen.findAllByRole("switch", { name: "settingsPage.knowledge.enableAria" });
    const enabledSwitch = switches.find((control) => !control.hasAttribute("disabled"));
    expect(enabledSwitch).toBeDefined();
    fireEvent.click(enabledSwitch!);

    await waitFor(() => expect(mocks.disableTool).toHaveBeenCalledWith("multimodal"));
    expect(mocks.navigate).not.toHaveBeenCalled();
  });

  it("opens knowledge bases from the entire row and preserves the settings return context", async () => {
    render(<KnowledgeDataSettings
      controlsDisabled={false}
      documentParsingEnabled={false}
      documentParsingSaving={false}
      headingRef={createRef<HTMLHeadingElement>()}
      onDocumentParsingChange={vi.fn()}
    />);

    const knowledgeRow = await screen.findByRole("link", { name: "打开知识库配置" });
    fireEvent.click(knowledgeRow);

    expect(mocks.navigate).toHaveBeenCalledWith("/lib/knowledge/list?from=settings-knowledge");
  });

  it("uses the reviewed localized copy instead of a backend tool description", async () => {
    mocks.listToolAssetsPage.mockResolvedValueOnce({
      records: [{
        id: "kb",
        name: "知识库",
        description: "后端旧描述",
        isEnabled: true,
        readonly: false,
      }],
    });

    render(<KnowledgeDataSettings
      controlsDisabled={false}
      documentParsingEnabled={false}
      documentParsingSaving={false}
      headingRef={createRef<HTMLHeadingElement>()}
      onDocumentParsingChange={vi.fn()}
    />);

    expect(await screen.findByText(
      "知识库广场、知识库中查找文档、统计信息和相关片段，用于基于资料回答问题。",
    )).toBeInTheDocument();
    expect(screen.queryByText("后端旧描述")).not.toBeInTheDocument();
  });

  it("shows parsing providers inline and keeps the parsing switch in the group header", async () => {
    const onDocumentParsingChange = vi.fn();
    render(<KnowledgeDataSettings
      controlsDisabled={false}
      documentParsingEnabled={false}
      documentParsingSaving={false}
      headingRef={createRef<HTMLHeadingElement>()}
      onDocumentParsingChange={onDocumentParsingChange}
    />);

    const parsingSwitch = await screen.findByRole("switch", { name: "settingsPage.knowledge.documentParsingAria" });
    expect(parsingSwitch.closest(".settings-knowledge-group-head")).not.toBeNull();
    expect(screen.getByRole("button", { name: "配置 MinerU" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "配置 PaddleOCR" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "settingsPage.knowledge.openParsingAria" })).not.toBeInTheDocument();

    fireEvent.click(parsingSwitch);
    expect(onDocumentParsingChange).toHaveBeenCalledWith(true, expect.anything());
  });
});
