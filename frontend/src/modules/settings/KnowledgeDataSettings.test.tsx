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

vi.mock("react-i18next", async (importOriginal) => ({
  ...await importOriginal<typeof import("react-i18next")>(),
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      if (key === "settingsPage.knowledge.groups.recognition.multimodal.name") return "多模态识别";
      if (key === "settingsPage.knowledge.groups.retrieval.kb.name") return "知识库";
      if (key === "settingsPage.knowledge.groups.retrieval.kb.description") {
        return "知识库广场、知识库中查找文档、统计信息和相关片段，用于基于资料回答问题。";
      }
      if (key === "settingsPage.enabled") return "已启用";
      if (key === "settingsPage.disabled") return "已停用";
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
      documentDirectoriesEnabled={true}
      documentDirectoriesSaving={false}
      onDocumentDirectoriesChange={vi.fn()}
      headingRef={createRef<HTMLHeadingElement>()}
    />);

    fireEvent.click(await screen.findByRole("link", { name: "打开多模态识别配置" }));

    expect(mocks.navigate).toHaveBeenCalledWith("/settings?section=models");
  });

  it("keeps the nested switch independent from row navigation", async () => {
    render(<KnowledgeDataSettings
      controlsDisabled={false}
      documentDirectoriesEnabled={true}
      documentDirectoriesSaving={false}
      onDocumentDirectoriesChange={vi.fn()}
      headingRef={createRef<HTMLHeadingElement>()}
    />);

    const switches = await screen.findAllByRole("switch", { name: "settingsPage.knowledge.enableAria" });
    const enabledSwitch = switches.find((control) => !control.hasAttribute("disabled"));
    expect(enabledSwitch).toBeDefined();
    fireEvent.click(enabledSwitch!);

    await waitFor(() => expect(mocks.disableTool).toHaveBeenCalledWith("multimodal"));
    expect(mocks.navigate).not.toHaveBeenCalled();
  });

  it("keeps knowledge, data and file rows in settings while search links to default models", async () => {
    render(<KnowledgeDataSettings
      controlsDisabled={false}
      documentDirectoriesEnabled={true}
      documentDirectoriesSaving={false}
      onDocumentDirectoriesChange={vi.fn()}
      headingRef={createRef<HTMLHeadingElement>()}
    />);

    fireEvent.click(await screen.findByText("知识库"));
    expect(screen.queryByRole("link", { name: "打开知识库配置" })).not.toBeInTheDocument();
    for (const group of ["retrieval", "data", "file-access"]) {
      const row = document.querySelector(`.settings-knowledge-group.is-${group}`)!;
      expect(row.querySelector("a")).toBeNull();
    }
    expect(document.querySelector('a[href="/settings?section=model_defaults&service=search"]')).not.toBeNull();
    expect(document.querySelector('a[href="/settings?section=model_defaults&service=academic"]')).not.toBeNull();
    expect(document.querySelector(".settings-knowledge-group.is-translation")).toBeNull();
    expect(document.querySelector(".settings-knowledge-group.is-parser")).toBeNull();
    expect(mocks.navigate).not.toHaveBeenCalled();
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
      documentDirectoriesEnabled={true}
      documentDirectoriesSaving={false}
      onDocumentDirectoriesChange={vi.fn()}
      headingRef={createRef<HTMLHeadingElement>()}
    />);

    expect(await screen.findByText(
      "知识库广场、知识库中查找文档、统计信息和相关片段，用于基于资料回答问题。",
    )).toBeInTheDocument();
    expect(screen.queryByText("后端旧描述")).not.toBeInTheDocument();
  });

});

it("shows the independent directory switch and forwards changes", async () => {
 const changed = vi.fn();
 const { rerender } = render(<KnowledgeDataSettings controlsDisabled={false} documentDirectoriesEnabled documentDirectoriesSaving={false} onDocumentDirectoriesChange={changed} headingRef={createRef<HTMLHeadingElement>()} />);
 const toggle = await screen.findByRole("switch", { name: "settingsPage.controls.documentDirectories.title" });
 expect(toggle).toHaveAttribute("aria-checked", "true");
 fireEvent.click(toggle);
 expect(changed).toHaveBeenCalledWith(false, expect.anything());
 rerender(<KnowledgeDataSettings controlsDisabled={false} documentDirectoriesEnabled={false} documentDirectoriesSaving={false} onDocumentDirectoriesChange={changed} headingRef={createRef<HTMLHeadingElement>()} />);
 expect(toggle).toHaveAttribute("aria-checked", "false");
});
