import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, useLocation } from "react-router-dom";
import { useChatInputStore } from "@/modules/chat/store/chatInput";
import { desktopTestTranslation } from "@/test/desktopResourceFixtures";

import SkillAdminPublishModal from "./SkillAdminPublishModal";

const skillApiMocks = vi.hoisted(() => ({
  publishSkillToMarket: vi.fn(),
}));

vi.mock("../../skillApi", () => skillApiMocks);

const labels: Record<string, string> = {
  "admin.memorySkillAdminPublishTitle": "管理员上架技能",
  "admin.memorySkillAdminPublishDesc": "上架到技能广场，供团队成员查看和安装。",
  "admin.memorySkillAdminPublishMethodLink": "仓库链接",
  "admin.memorySkillAdminPublishMethodPackage": "本地上传",
  "admin.memorySkillAdminPublishSubmit": "上架技能",
  "admin.memorySkillUploadRepoLabel": "仓库链接",
  "admin.memorySkillUploadRepoPlaceholder": "请输入仓库链接",
  "admin.memorySkillMarketTags": "技能标签",
  "admin.memoryTagsPlaceholder": "请输入标签",
  "admin.memorySkillAdminPublishSuccessAuto": "技能已上架",
  "common.cancel": "取消",
};

const t = (key: string, options?: Record<string, unknown>) => labels[key] || desktopTestTranslation(key, options);

function Location() {
  return <output aria-label="Current route">{useLocation().pathname}</output>;
}

function mount() {
  return render(<MemoryRouter><SkillAdminPublishModal
    open t={t} onClose={vi.fn()} onPublished={vi.fn().mockResolvedValue(undefined)}
  /><Location /></MemoryRouter>);
}

beforeAll(() => {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
});

describe("SkillAdminPublishModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useChatInputStore.getState().setModeDraft();
    skillApiMocks.publishSkillToMarket.mockResolvedValue({
      marketItemId: "market-1",
      sourceSkillId: "skill-1",
    });
  });

  it.each([
    "https://github.com/example/skill",
    "https://github.com/example/skills/tree/main/packages/writer",
    "https://github.com/example/skills/blob/main/packages/writer/SKILL.md",
  ])("preserves the GitHub source URL %s for server-side skill discovery", async (url) => {
    const onPublished = vi.fn().mockResolvedValue(undefined);

    render(
      <MemoryRouter>
        <SkillAdminPublishModal
          open
          t={t}
          onClose={vi.fn()}
          onPublished={onPublished}
        />
      </MemoryRouter>,
    );

    expect(screen.queryByText("技能标签")).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("请输入标签")).not.toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("请输入仓库链接"), {
      target: { value: ` ${url} ` },
    });
    fireEvent.click(screen.getByRole("button", { name: "上架技能" }));

    await waitFor(() => {
      expect(skillApiMocks.publishSkillToMarket).toHaveBeenCalledWith({
        source: {
          type: "url",
          url,
        },
      }, { silentError: true });
    });
    expect(onPublished).toHaveBeenCalledTimes(1);
  });

  it("offers the original URL as an editable chat draft after oversized publishing fails", async () => {
    skillApiMocks.publishSkillToMarket.mockRejectedValueOnce({ response: { status: 413, data: { data: { code: "payload_too_large" } } } });
    mount();
    const url = "https://github.com/example/large/tree/release/writer?plain=1#readme";
    fireEvent.change(screen.getByPlaceholderText("请输入仓库链接"), { target: { value: ` ${url} ` } });
    fireEvent.click(screen.getByRole("button", { name: "上架技能" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("导入大小限制");
    expect(screen.getByPlaceholderText("请输入仓库链接")).toHaveValue(` ${url} `);
    fireEvent.click(within(alert).getByRole("button", { name: "去对话中安装" }));
    expect(screen.getByLabelText("Current route")).toHaveTextContent("/agent/chat/home");
    expect(useChatInputStore.getState().modeDraft).toEqual({ text: `请帮我从以下链接安装技能：\n${url}`, mentions: [], files: [] });
  });

  it("explains which concrete link to provide after an ambiguous repository publish fails", async () => {
    skillApiMocks.publishSkillToMarket.mockRejectedValueOnce({ response: { status: 400, data: { data: { code: "github_multiple_skills" } } } });
    mount();
    fireEvent.change(screen.getByPlaceholderText("请输入仓库链接"), { target: { value: "https://github.com/example/skills" } });
    fireEvent.click(screen.getByRole("button", { name: "上架技能" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("具体技能目录或 SKILL.md 链接");
    expect(screen.queryByRole("button", { name: "去对话中安装" })).not.toBeInTheDocument();
  });
});
