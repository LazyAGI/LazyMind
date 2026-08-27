import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

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

const t = (key: string) => labels[key] || key;

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
    skillApiMocks.publishSkillToMarket.mockResolvedValue({
      marketItemId: "market-1",
      sourceSkillId: "skill-1",
    });
  });

  it("publishes a GitHub repository archive without a tag field or tag payload", async () => {
    const onPublished = vi.fn().mockResolvedValue(undefined);

    render(
      <SkillAdminPublishModal
        open
        t={t}
        onClose={vi.fn()}
        onPublished={onPublished}
      />,
    );

    expect(screen.queryByText("技能标签")).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("请输入标签")).not.toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("请输入仓库链接"), {
      target: { value: " https://github.com/example/skill " },
    });
    fireEvent.click(screen.getByRole("button", { name: "上架技能" }));

    await waitFor(() => {
      expect(skillApiMocks.publishSkillToMarket).toHaveBeenCalledWith({
        source: {
          type: "url",
          url: "https://github.com/example/skill/archive/HEAD.zip",
        },
      });
    });
    expect(onPublished).toHaveBeenCalledTimes(1);
  });
});
