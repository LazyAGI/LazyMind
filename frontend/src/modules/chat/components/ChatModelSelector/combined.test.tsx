import { useState } from "react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { message } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ThinkingDepth } from "@/modules/chat/store/chatThink";
import {
  useModelSelectionStore,
  type ChatModelCatalog,
} from "@/modules/chat/store/modelSelection";
import ChatModelSelector from ".";
import { fetchChatModelCatalog, updateConversationChatModel } from "./api";

const localeState = vi.hoisted(() => ({ language: "zh-CN" }));

vi.mock("react-i18next", async () => {
  const zh = (await import("@/i18n/locales/zh-CN")).default;
  const en = (await import("@/i18n/locales/en-US")).default;
  return ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, string>) => {
      const locale = localeState.language === "zh-CN" ? zh : en;
      if (key.startsWith("chat.thinkingDepthLabels.")) {
        return locale.chat.thinkingDepthLabels[key.split(".")[2] as ThinkingDepth];
      }
      const labels: Record<string, string> = {
        "chat.modelThinkingSelectorLabel": "模型与思考深度",
        "chat.modelSelectorDialogLabel": "对话模型选择",
        "chat.modelSelectorLoading": "模型信息加载中",
        "chat.modelSelectorLoadFailed": "模型信息加载失败，请重试",
        "chat.modelSelectorRetry": "重试",
        "chat.modelSelectorReload": "重新加载",
        "chat.modelSelectorChoose": "选择模型",
        "chat.modelSelectorSearchLabel": "搜索对话模型",
        "chat.modelSelectorSearchPlaceholder": "搜索模型",
        "chat.modelSelectorAutoDescription": "自动选择",
        "chat.modelSelectorSwitchFailed": "模型切换失败，请重新加载后再试",
        "chat.modelSelectorSwitching": "正在切换模型",
        "chat.modelSelectorWorkflowRunning": "Workflow 正在执行，暂时无法切换模型",
        "chat.thinkingDepth": "思考深度",
        "chat.thinkingDepthReset": "重置思考深度为中",
      };
      if (key === "chat.modelThinkingSelectorTriggerLabel") {
        return `当前模型：${params?.model}，思考深度：${params?.depth}`;
      }
      if (key === "chat.modelSelectorTriggerLabel") {
        return `当前模型：${params?.model}`;
      }
      if (key === "chat.modelSelectorSwitched") {
        return `已切换至${params?.model}`;
      }
      return labels[key] ?? key;
    },
  }),
});
});

vi.mock("./api", () => ({
  fetchChatModelCatalog: vi.fn(),
  updateConversationChatModel: vi.fn(),
}));

const fetchCatalogMock = vi.mocked(fetchChatModelCatalog);
const updateSelectionMock = vi.mocked(updateConversationChatModel);

function catalog(): ChatModelCatalog {
  return {
    selection: {
      mode: "fixed",
      model_id: "deepseek-v4-pro",
      provider_name: "DeepSeek",
      model_name: "deepseek-v4-pro",
      version: 3,
    },
    default_selection: { mode: "auto", version: 0 },
    providers: [
      {
        id: "deepseek",
        name: "DeepSeek",
        models: [{ id: "deepseek-v4-pro", name: "deepseek-v4-pro", available: true }],
      },
      {
        id: "openai",
        name: "OpenAI",
        models: [{ id: "gpt-4o", name: "GPT-4o", available: true }],
      },
    ],
    switch_allowed: true,
    auto_available: true,
  };
}

async function openControls(depth = "最高") {
  const trigger = await screen.findByRole("button", {
    name: `当前模型：DeepSeek · deepseek-v4-pro，思考深度：${depth}`,
  });
  fireEvent.click(trigger);
  return trigger;
}

async function openModels() {
  await openControls();
  fireEvent.click(screen.getByRole("button", { name: "选择模型" }));
  return screen.findByRole("button", { name: "GPT-4o" });
}

describe("ChatModelSelector combined thinking depth controls", () => {
  beforeEach(() => {
    localeState.language = "zh-CN";
    fetchCatalogMock.mockResolvedValue(catalog());
    vi.spyOn(message, "success").mockImplementation(() => undefined as never);
    vi.spyOn(message, "warning").mockImplementation(() => undefined as never);
  });

  afterEach(() => {
    useModelSelectionStore.setState({ selections: {} });
    vi.resetAllMocks();
    vi.restoreAllMocks();
  });

  it("uses one compact entry and changes controlled depth without saving a model", async () => {
    const onDepthChange = vi.fn();
    function ControlledSelector() {
      const [depth, setDepth] = useState<ThinkingDepth>("medium");
      return (
        <ChatModelSelector
          conversationId="conversation-1"
          thinkingDepth={depth}
          onThinkingDepthChange={(next) => {
            onDepthChange(next);
            setDepth(next);
          }}
        />
      );
    }
    render(<ControlledSelector />);
    await openControls("中");

    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    const slider = screen.getByRole("slider", { name: "思考深度" });
    fireEvent.change(slider, { target: { value: "3" } });
    expect(onDepthChange).not.toHaveBeenCalled();
    fireEvent.pointerUp(slider);

    expect(onDepthChange).toHaveBeenLastCalledWith("max");
    expect(
      screen.getByRole("button", {
        name: "当前模型：DeepSeek · deepseek-v4-pro，思考深度：最高",
      }),
    ).toBeInTheDocument();
    expect(updateSelectionMock).not.toHaveBeenCalled();
  });

  it.each([
    { language: "zh-CN", maxLabel: "最高", stops: ["低", "中", "高", "最高"] },
    { language: "en-US", maxLabel: "Max", stops: ["Low", "Medium", "High", "Max"] },
  ])("uses $language translations for depth labels and accessible values", async ({ language, maxLabel, stops }) => {
    localeState.language = language;
    render(<ChatModelSelector thinkingDepth="max" onThinkingDepthChange={vi.fn()} />);
    await openControls(maxLabel);
    expect(screen.getByRole("slider")).toHaveAttribute("aria-valuetext", maxLabel);
    for (const label of stops) {
      expect(within(screen.getByRole("dialog")).getAllByText(label).length).toBeGreaterThan(0);
    }
  });

  it("resets depth to medium without changing the selected model", async () => {
    const onDepthChange = vi.fn();
    render(
      <ChatModelSelector thinkingDepth="max" onThinkingDepthChange={onDepthChange} />,
    );
    await openControls();
    fireEvent.click(screen.getByRole("button", { name: "重置思考深度为中" }));

    expect(onDepthChange).toHaveBeenCalledWith("medium");
    expect(updateSelectionMock).not.toHaveBeenCalled();
  });

  it("expands the model list and preserves the versioned save and parent send lock", async () => {
    const onSavingChange = vi.fn();
    const onSelectionChange = vi.fn();
    const onDepthChange = vi.fn();
    updateSelectionMock.mockResolvedValue({
      mode: "fixed",
      model_id: "gpt-4o",
      provider_name: "OpenAI",
      model_name: "GPT-4o",
      version: 4,
    });
    render(
      <ChatModelSelector
        conversationId="conversation-1"
        thinkingDepth="max"
        onThinkingDepthChange={onDepthChange}
        onSavingChange={onSavingChange}
        onSelectionChange={onSelectionChange}
      />,
    );
    fireEvent.click(await openModels());

    expect(
      await screen.findByRole("button", {
        name: "当前模型：OpenAI · GPT-4o，思考深度：最高",
      }),
    ).toBeInTheDocument();
    expect(updateSelectionMock).toHaveBeenCalledWith(
      "conversation-1",
      { mode: "fixed", model_id: "gpt-4o" },
      3,
      expect.any(AbortSignal),
    );
    expect(onSavingChange.mock.calls).toEqual([[true], [false]]);
    expect(onSelectionChange).toHaveBeenLastCalledWith(
      { mode: "fixed", model_id: "gpt-4o" },
      expect.objectContaining({ model_id: "gpt-4o", version: 4 }),
    );
    expect(onDepthChange).not.toHaveBeenCalled();
  });

  it("keeps both current values when a model switch fails and exposes recovery", async () => {
    const onSavingChange = vi.fn();
    updateSelectionMock.mockRejectedValue(new Error("conflict"));
    render(
      <ChatModelSelector
        conversationId="conversation-1"
        thinkingDepth="max"
        onThinkingDepthChange={vi.fn()}
        onSavingChange={onSavingChange}
      />,
    );
    fireEvent.click(await openModels());

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "模型切换失败，请重新加载后再试",
    );
    expect(
      screen.getByRole("button", {
        name: "当前模型：DeepSeek · deepseek-v4-pro，思考深度：最高",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重新加载" })).toBeEnabled();
    expect(onSavingChange.mock.calls).toEqual([[true], [false]]);
  });

  it("disables fixed depth controls while keeping model selection available", async () => {
    const onDepthChange = vi.fn();
    render(
      <ChatModelSelector
        thinkingDepth="max"
        thinkingDepthDisabled
        onThinkingDepthChange={onDepthChange}
      />,
    );
    await openControls();

    expect(screen.getByRole("slider", { name: "思考深度" })).toBeDisabled();
    const reset = screen.getByRole("button", { name: "重置思考深度为中" });
    expect(reset).toBeDisabled();
    fireEvent.click(reset);
    expect(onDepthChange).not.toHaveBeenCalled();
    const chooseModel = screen.getByRole("button", { name: "选择模型" });
    expect(chooseModel).toBeEnabled();
    fireEvent.click(chooseModel);
    expect(screen.getByRole("button", { name: "GPT-4o" })).toBeEnabled();
  });

  it("preserves model search after expanding the compact controls", async () => {
    render(<ChatModelSelector thinkingDepth="max" onThinkingDepthChange={vi.fn()} />);
    await openModels();
    const search = screen.getByRole("searchbox", { name: "搜索对话模型" });
    expect(screen.queryByRole("slider")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "重置思考深度为中" })).not.toBeInTheDocument();
    fireEvent.change(search, { target: { value: "OpenAI" } });

    expect(screen.getByRole("button", { name: "GPT-4o" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "deepseek-v4-pro" })).not.toBeInTheDocument();
    fireEvent.keyDown(search, { key: "Escape" });
    await openControls();
    expect(screen.getByRole("slider", { name: "思考深度" })).toBeInTheDocument();
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
  });

  it("keeps depth adjustable while a running workflow blocks model switching", async () => {
    const onDepthChange = vi.fn();
    render(
      <ChatModelSelector
        thinkingDepth="max"
        onThinkingDepthChange={onDepthChange}
        disabled
        disabledReason="Workflow 正在执行，暂时无法切换模型"
      />,
    );
    await openControls();

    const chooseModel = screen.getByRole("button", { name: "选择模型" });
    expect(chooseModel).toBeDisabled();
    fireEvent.click(chooseModel);
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
    expect(screen.getByRole("slider", { name: "思考深度" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "重置思考深度为中" }));
    expect(onDepthChange).toHaveBeenCalledWith("medium");
    expect(updateSelectionMock).not.toHaveBeenCalled();
  });

  it("blocks the combined entry when generation disables both controls", async () => {
    render(
      <ChatModelSelector
        thinkingDepth="max"
        onThinkingDepthChange={vi.fn()}
        disabled
        thinkingDepthDisabled
      />,
    );
    const trigger = await openControls();

    expect(trigger).toHaveAttribute("aria-disabled", "true");
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(updateSelectionMock).not.toHaveBeenCalled();
  });

  it("stops model switches if a workflow starts after the model list is opened", async () => {
    const onDepthChange = vi.fn();
    const { rerender } = render(
      <ChatModelSelector
        conversationId="conversation-1"
        thinkingDepth="max"
        onThinkingDepthChange={onDepthChange}
      />,
    );
    await openModels();
    rerender(
      <ChatModelSelector
        conversationId="conversation-1"
        thinkingDepth="max"
        onThinkingDepthChange={onDepthChange}
        disabled
        disabledReason="Workflow 正在执行，暂时无法切换模型"
      />,
    );

    const option = screen.queryByRole("button", { name: "GPT-4o" });
    if (option) {
      expect(option).toBeDisabled();
      fireEvent.click(option);
    }
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    await openControls();
    expect(screen.getByRole("slider", { name: "思考深度" })).toBeEnabled();
    expect(updateSelectionMock).not.toHaveBeenCalled();
  });

  it("closes with Escape and returns focus to the combined entry", async () => {
    render(<ChatModelSelector thinkingDepth="max" onThinkingDepthChange={vi.fn()} />);
    const trigger = await openControls();
    const dialog = screen.getByRole("dialog", { name: "模型与思考深度" });
    const slider = within(dialog).getByRole("slider", { name: "思考深度" });
    slider.focus();
    fireEvent.keyDown(slider, { key: "Escape" });

    await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "false"));
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
