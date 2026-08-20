import { createRef } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import KnowledgeDataSettings from "./KnowledgeDataSettings";

const mocks = vi.hoisted(() => ({
  listToolAssetsPage: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      if (key === "settingsPage.knowledge.groups.recognition.multimodal.name") return "多模态识别";
      if (key === "settingsPage.knowledge.openConfigAria") return `打开${values?.name}配置`;
      return key;
    },
  }),
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => mocks.navigate,
}));

vi.mock("@/modules/memory/toolApi", () => ({
  disableTool: vi.fn(),
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

    fireEvent.click(await screen.findByRole("button", { name: "打开多模态识别配置" }));

    expect(mocks.navigate).toHaveBeenCalledWith("/settings?section=models");
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
