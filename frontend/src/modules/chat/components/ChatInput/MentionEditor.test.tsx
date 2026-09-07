import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import MentionEditor from "./MentionEditor";

type ScrollablePrototype = typeof HTMLElement.prototype & {
  scrollTo?: (...args: unknown[]) => void;
};

const scrollablePrototype = HTMLElement.prototype as ScrollablePrototype;
const originalScrollTo = scrollablePrototype.scrollTo;

const mocks = vi.hoisted(() => ({
  listSkillAssetsPage: vi.fn(),
  listToolAssetsPage: vi.fn(),
  listDatasets: vi.fn(),
  listPrompts: vi.fn(),
  listConversations: vi.fn(),
  axiosGet: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/memory/skillApi", () => ({
  listSkillAssetsPage: mocks.listSkillAssetsPage,
}));

vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssetsPage: mocks.listToolAssetsPage,
}));

vi.mock("@/components/request", () => ({
  BASE_URL: "",
  axiosInstance: { get: mocks.axiosGet },
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceListConversations: mocks.listConversations,
  }),
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: mocks.listDatasets,
  }),
  PromptServiceApi: () => ({
    listPrompts: mocks.listPrompts,
  }),
}));

describe("MentionEditor", () => {
  beforeEach(() => {
    Object.defineProperty(scrollablePrototype, "scrollTo", {
      configurable: true,
      value: vi.fn(),
    });

    mocks.listSkillAssetsPage.mockReset();
    mocks.listToolAssetsPage.mockReset();
    mocks.listDatasets.mockReset();
    mocks.listPrompts.mockReset();
    mocks.listConversations.mockReset();
    mocks.axiosGet.mockReset();

    mocks.listSkillAssetsPage.mockResolvedValue({ records: [] });
    mocks.listToolAssetsPage.mockResolvedValue({ records: [] });
    mocks.listDatasets.mockResolvedValue({ data: { datasets: [] } });
    mocks.listPrompts.mockResolvedValue({ data: { prompts: [] } });
    mocks.listConversations.mockResolvedValue({ data: { conversations: [] } });
    mocks.axiosGet.mockResolvedValue({ data: { workflows: [] } });
  });

  afterEach(() => {
    window.getSelection()?.removeAllRanges();
    if (originalScrollTo) {
      Object.defineProperty(scrollablePrototype, "scrollTo", {
        configurable: true,
        value: originalScrollTo,
      });
    } else {
      Reflect.deleteProperty(scrollablePrototype, "scrollTo");
    }
    vi.restoreAllMocks();
  });

  it("reloads skills after a previously cached empty list", async () => {
    render(
      <MentionEditor
        value=""
        placeholder="message"
        onChange={vi.fn()}
        onMentionsChange={vi.fn()}
        onPaste={vi.fn()}
        onSend={vi.fn()}
        onCompositionChange={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(mocks.listSkillAssetsPage).toHaveBeenCalledTimes(1);
    });

    mocks.listSkillAssetsPage.mockResolvedValue({
      records: [
        {
          id: "skill-new",
          name: "新增技能",
          description: "刚刚添加的技能",
        },
      ],
    });

    const editor = screen.getByRole("textbox");
    editor.textContent = "@skill:";
    const range = document.createRange();
    const textNode = editor.firstChild;
    if (!textNode) {
      throw new Error("Mention editor did not create a text node");
    }
    range.setStart(textNode, textNode.textContent?.length || 0);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);

    expect(await screen.findByRole("option", { name: "新增技能" })).toBeInTheDocument();
    expect(mocks.listSkillAssetsPage).toHaveBeenCalledTimes(2);
  });

  it("reloads workflows so newly published workflows are shown", async () => {
    render(
      <MentionEditor
        value=""
        placeholder="message"
        onChange={vi.fn()}
        onMentionsChange={vi.fn()}
        onPaste={vi.fn()}
        onSend={vi.fn()}
        onCompositionChange={vi.fn()}
      />,
    );

    await waitFor(() => expect(mocks.axiosGet).toHaveBeenCalledTimes(1));
    mocks.axiosGet.mockResolvedValue({
      data: {
        workflows: [{
          workflow_ref: "user:user-1:ppt-workflow-copy",
          workflow_id: "ppt-workflow-copy",
          name: "AI PPT 规划 副本",
          description: "",
        }],
      },
    });

    const editor = screen.getByRole("textbox");
    editor.textContent = "@workflow:";
    const textNode = editor.firstChild;
    if (!textNode) throw new Error("Mention editor did not create a text node");
    const range = document.createRange();
    range.setStart(textNode, textNode.textContent?.length || 0);
    range.collapse(true);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.input(editor);

    expect(await screen.findByRole("option", { name: "AI PPT 规划 副本" })).toBeInTheDocument();
    expect(mocks.axiosGet).toHaveBeenCalledTimes(2);
  });
});
