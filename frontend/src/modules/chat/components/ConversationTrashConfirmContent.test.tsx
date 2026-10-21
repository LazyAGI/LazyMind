import { render, screen } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { describe, expect, it } from "vitest";
import zhCN from "@/i18n/locales/zh-CN";
import enUS from "@/i18n/locales/en-US";
import ConversationTrashConfirmContent from "./ConversationTrashConfirmContent";

async function renderContent(name?: string, showForkNotice = false, lng = "zh-CN") {
  const i18n = createInstance();
  await i18n.init({ lng, resources: { "zh-CN": { translation: zhCN }, "en-US": { translation: enUS } } });
  return render(<I18nextProvider i18n={i18n}><ConversationTrashConfirmContent name={name} showForkNotice={showForkNotice} /></I18nextProvider>);
}

describe("ConversationTrashConfirmContent", () => {
  it.each([
    "很长的中文对话名称".repeat(30),
    "LongConversationName".repeat(30),
    "https://example.test/" + "a".repeat(500),
    "第一行\n第二行\n第三行",
    '<img src=x onerror="alert(1)">',
  ])("keeps the full name as plain text for the two-line preview: %#", async name => {
    const { container } = await renderContent(name);
    const preview = screen.getByTitle(name, { normalizer: value => value });
    expect(preview.textContent).toBe(name);
    expect(preview).toHaveClass("conversation-trash-confirm-name");
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText(zhCN.settingsPage.recovery.moveToTrashDescription)).toBeInTheDocument();
    expect(screen.queryByText(zhCN.chat.fork.deleteNotice)).not.toBeInTheDocument();
  });

  it.each([undefined, "", " \n "])("uses the unnamed-conversation fallback for %s", async name => {
    await renderContent(name);
    expect(screen.getByText("未命名对话")).toHaveAttribute("title", "未命名对话");
  });

  it("preserves the fork notice separately from the name and retention message", async () => {
    await renderContent("有分支的对话", true);
    expect(screen.getByText(zhCN.chat.fork.deleteNotice)).toBeInTheDocument();
  });

  it("provides the English fallback and full retention message", async () => {
    await renderContent(undefined, false, "en-US");
    expect(screen.getByText("Untitled conversation")).toBeInTheDocument();
    expect(screen.getByText(enUS.settingsPage.recovery.moveToTrashDescription)).toBeInTheDocument();
  });
});
