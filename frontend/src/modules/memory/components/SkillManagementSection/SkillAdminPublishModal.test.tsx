import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { message } from "antd";
import { MemoryRouter, useLocation } from "react-router-dom";
import { useChatInputStore } from "@/modules/chat/store/chatInput";
import { desktopTestTranslation } from "@/test/desktopResourceFixtures";

import SkillAdminPublishModal from "./SkillAdminPublishModal";

const skillApiMocks = vi.hoisted(() => ({
  publishSkillToMarket: vi.fn(),
  uploadSkillTempFile: vi.fn(),
}));

vi.mock("../../skillApi", () => skillApiMocks);
vi.mock("../../skillUpload", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../skillUpload")>()), uploadSkillTempFile: skillApiMocks.uploadSkillTempFile }));

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


  it("clears a source error when corrected and publishes on retry", async () => {
    skillApiMocks.publishSkillToMarket.mockRejectedValueOnce({ response: { status: 400, data: { data: { code: "github_multiple_skills" } } } });
    const onClose = vi.fn();
    render(<MemoryRouter><SkillAdminPublishModal open t={t} onClose={onClose} onPublished={vi.fn().mockResolvedValue(undefined)} /></MemoryRouter>);
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "https://github.com/example/skills" } });
    fireEvent.click(screen.getByRole("button", { name: "上架技能" }));
    await screen.findByRole("alert");
    fireEvent.change(input, { target: { value: "https://github.com/example/skills/tree/main/writer" } });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(input).toHaveAttribute("aria-invalid", "false");
    fireEvent.click(screen.getByRole("button", { name: /上架技能$/ }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });

  it("can cancel a pending URL request and ignores its late success", async () => {
    let finish!: (value: unknown) => void;
    skillApiMocks.publishSkillToMarket.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    const onPublished = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(<MemoryRouter><SkillAdminPublishModal open t={t} onClose={onClose} onPublished={onPublished} /></MemoryRouter>);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "https://github.com/example/skill" } });
    fireEvent.click(screen.getByRole("button", { name: "上架技能" }));
    const signal = skillApiMocks.publishSkillToMarket.mock.calls[0]?.[1]?.signal as AbortSignal;
    expect(screen.getByRole("status")).toHaveTextContent("导入");
    expect(screen.getByRole("button", { name: /取\s*消/ })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: /取\s*消/ }));
    expect(signal.aborted).toBe(true);
    expect(onClose).toHaveBeenCalledTimes(1);
    await act(async () => { finish({ marketItemId: "late", sourceSkillId: "late" }); });
    expect(onPublished).not.toHaveBeenCalled();
  });

  it("freezes selected files and shows progress while uploading", async () => {
    skillApiMocks.uploadSkillTempFile.mockImplementationOnce((_file, options) => {
      options.onProgress(40);
      return new Promise(() => {});
    });
    mount();
    fireEvent.click(screen.getByRole("tab", { name: "本地上传" }));
    fireEvent.change(document.querySelector('input[type="file"]')!, { target: { files: [new File(["zip"], "writer.zip")] } });
    await screen.findByText("writer.zip");
    fireEvent.click(screen.getByRole("button", { name: "上架技能" }));
    await waitFor(() => expect(within(screen.getByRole("dialog")).getByRole("status")).toHaveTextContent("40"));
    expect(document.querySelector('input[type="file"]')).toBeDisabled();
    expect(screen.getByRole("button", { name: t("common.delete") })).toBeDisabled();
  });

  it("rejects non-ZIP or oversized packages before publishing", async () => {
    mount();
    fireEvent.click(screen.getByRole("tab", { name: "本地上传" }));
    const tooLarge = new File([], "large.zip");
    Object.defineProperty(tooLarge, "size", { value: 104857601 });
    for (const file of [new File(["tar"], "writer.tgz"), tooLarge]) {
      fireEvent.change(document.querySelector('input[type="file"]')!, { target: { files: [file] } });
      await act(async () => {});
      expect(screen.queryByText(file.name)).not.toBeInTheDocument();
    }
    expect(skillApiMocks.uploadSkillTempFile).not.toHaveBeenCalled();
  });

  it("reports a completed publish as success even when refreshing the catalog fails", async () => {
    const onClose = vi.fn();
    const errorToast = vi.spyOn(message, "error");
    render(<MemoryRouter><SkillAdminPublishModal open t={t} onClose={onClose} onPublished={vi.fn().mockRejectedValue(new Error("refresh failed"))} /></MemoryRouter>);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "https://github.com/example/skill" } });
    fireEvent.click(screen.getByRole("button", { name: "上架技能" }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(errorToast).not.toHaveBeenCalled();
    errorToast.mockRestore();
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
      }, expect.objectContaining({ silentError: true, signal: expect.anything() }));
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
