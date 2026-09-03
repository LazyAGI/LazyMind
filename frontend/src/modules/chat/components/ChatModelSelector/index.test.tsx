import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { message } from "antd";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CHAT_OPEN_MODEL_SELECTOR_EVENT } from "@/modules/chat/constants/chat";
import {
  NEW_CHAT_MODEL_SELECTION_KEY,
  useModelSelectionStore,
  type ChatModelCatalog,
} from "@/modules/chat/store/modelSelection";
import ChatModelSelector from ".";
import {
  fetchChatModelCatalog,
  updateConversationChatModel,
} from "./api";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, string>) => {
      const labels: Record<string, string> = {
        "chat.modelSelectorTitle": "选择对话模型",
        "chat.modelSelectorDescription": "先选择供应商，再选择模型",
        "chat.modelSelectorDialogLabel": "对话模型选择",
        "chat.modelSelectorLoading": "模型信息加载中",
        "chat.modelSelectorLoadFailed": "模型信息加载失败，请重试",
        "chat.modelSelectorRetry": "重试",
        "chat.modelSelectorReload": "重新加载",
        "chat.modelSelectorUnavailable": "模型不可用",
        "chat.modelSelectorChoose": "选择模型",
        "chat.modelSelectorBusy": "当前任务执行中，暂时无法切换模型",
        "chat.modelSelectorGenerating": "正在生成回复，暂时无法切换模型",
        "chat.modelSelectorWorkflowRunning": "Workflow 正在执行，暂时无法切换模型",
        "chat.modelSelectorBackgroundTaskRunning": "后台任务正在执行，暂时无法切换模型",
        "chat.modelSelectorProvidersLabel": "模型供应商",
        "chat.modelSelectorSearchLabel": "搜索对话模型",
        "chat.modelSelectorSearchPlaceholder": "搜索模型",
        "chat.modelSelectorSearchEmpty": "没有匹配的模型",
        "chat.modelSelectorAutoDescription": "自动选择",
        "chat.modelSelectorCurrent": "当前使用",
        "chat.modelSelectorDefault": "默认",
        "chat.modelSelectorRecommended": "推荐",
        "chat.modelSelectorLowCost": "低成本",
        "chat.modelSelectorShared": "共享",
        "chat.modelSelectorCapabilityChat": "对话",
        "chat.modelSelectorProviderEmpty": "该供应商暂无可用的对话模型",
        "chat.modelSelectorEmpty": "暂无可用的对话模型",
        "chat.modelSelectorSwitching": "正在切换模型",
        "chat.modelSelectorSwitchFailed": "模型切换失败，请重新加载后再试",
      };
      if (key === "chat.modelSelectorTriggerLabel") {
        return `当前模型：${params?.model}`;
      }
      if (key === "chat.modelSelectorSwitched") {
        return `已切换至${params?.model}，将从下一条消息开始使用`;
      }
      return labels[key] ?? key;
    },
  }),
}));

vi.mock("./api", () => ({
  fetchChatModelCatalog: vi.fn(),
  updateConversationChatModel: vi.fn(),
}));

const fetchCatalogMock = vi.mocked(fetchChatModelCatalog);
const updateSelectionMock = vi.mocked(updateConversationChatModel);

function catalog(modelName = "DeepSeek-V3", version = 3): ChatModelCatalog {
  return {
    selection: {
      mode: "fixed",
      model_id: "deepseek-v3",
      provider_name: "DeepSeek",
      model_name: modelName,
      group_name: "默认连接",
      source: "own",
      version,
    },
    default_selection: {
      mode: "fixed",
      model_id: "deepseek-v3",
      provider_name: "DeepSeek",
      model_name: modelName,
      version: 0,
    },
    providers: [
      {
        id: "deepseek",
        name: "DeepSeek",
        models: [
          {
            id: "deepseek-v3",
            name: modelName,
            group_name: "默认连接",
            current: true,
            default: true,
            badges: ["default"],
            availability: "available",
            capabilities: ["chat"],
          },
        ],
      },
      {
        id: "openai",
        name: "OpenAI",
        models: [
          {
            id: "gpt-4o",
            name: "GPT-4o",
            group_name: "团队连接",
            shared: true,
            badges: ["shared"],
            availability: "available",
          },
        ],
      },
    ],
    switch_allowed: true,
    auto_available: true,
  };
}

describe("ChatModelSelector", () => {
  afterEach(() => {
    useModelSelectionStore.setState({ selections: {} });
    vi.clearAllMocks();
    vi.restoreAllMocks();
  });

  it("loads the historical selection and persists a provider-scoped model switch", async () => {
    fetchCatalogMock.mockResolvedValue(catalog());
    updateSelectionMock.mockResolvedValue({
      mode: "fixed",
      model_id: "gpt-4o",
      provider_name: "OpenAI",
      model_name: "GPT-4o",
      group_name: "团队连接",
      source: "shared",
      version: 4,
    });
    const onSelectionChange = vi.fn();
    const onSavingChange = vi.fn();
    const success = vi
      .spyOn(message, "success")
      .mockImplementation(() => undefined as never);

    render(
      <ChatModelSelector
        conversationId="conversation-1"
        onSavingChange={onSavingChange}
        onSelectionChange={onSelectionChange}
      />,
    );

    const trigger = await screen.findByRole("button", {
      name: "当前模型：DeepSeek · DeepSeek-V3",
    });
    expect(fetchCatalogMock).toHaveBeenCalledWith(
      "conversation-1",
      expect.any(AbortSignal),
    );

    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("button", { name: /GPT-4o/ }));

    await waitFor(() =>
      expect(updateSelectionMock).toHaveBeenCalledWith(
        "conversation-1",
        { mode: "fixed", model_id: "gpt-4o" },
        3,
        expect.any(AbortSignal),
      ),
    );
    expect(
      await screen.findByRole("button", {
        name: "当前模型：OpenAI · GPT-4o",
      }),
    ).toBeInTheDocument();
    expect(success).toHaveBeenCalledWith(
      "已切换至OpenAI · GPT-4o，将从下一条消息开始使用",
    );
    expect(onSelectionChange).toHaveBeenLastCalledWith(
      { mode: "fixed", model_id: "gpt-4o" },
      expect.objectContaining({ model_id: "gpt-4o", version: 4 }),
    );
    expect(onSavingChange.mock.calls).toEqual([[true], [false]]);
  });

  it("focuses search and filters models without mixing provider groups", async () => {
    fetchCatalogMock.mockResolvedValue(catalog());

    render(<ChatModelSelector conversationId="conversation-1" />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-V3",
      }),
    );

    const dialog = screen.getByRole("dialog", { name: "对话模型选择" });
    const search = within(dialog).getByRole("searchbox", {
      name: "搜索对话模型",
    });
    await waitFor(() => expect(search).toHaveFocus());
    expect(
      within(dialog).getByRole("region", { name: "DeepSeek" }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("region", { name: "OpenAI" }),
    ).toBeInTheDocument();

    fireEvent.change(search, { target: { value: "gpt" } });
    expect(
      within(dialog).queryByRole("region", { name: "DeepSeek" }),
    ).not.toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: /GPT-4o/ }),
    ).toBeInTheDocument();

    fireEvent.change(search, { target: { value: "not-a-model" } });
    expect(within(dialog).getByRole("status")).toHaveTextContent(
      "没有匹配的模型",
    );
  });

  it("shows an in-place load error and offers a safe retry", async () => {
    fetchCatalogMock
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(catalog());

    render(<ChatModelSelector conversationId="conversation-1" />);

    fireEvent.click(
      await screen.findByRole("button", { name: "当前模型：模型不可用" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "模型信息加载失败，请重试",
    );

    fireEvent.click(screen.getByRole("button", { name: "重试" }));

    expect(
      await screen.findByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-V3",
      }),
    ).toBeInTheDocument();
    expect(fetchCatalogMock).toHaveBeenCalledTimes(2);
  });

  it("keeps a new-chat Auto choice local for the first request", async () => {
    fetchCatalogMock.mockResolvedValue(catalog("DeepSeek-V3", 0));
    const onSelectionChange = vi.fn();
    vi.spyOn(message, "success").mockImplementation(() => undefined as never);

    render(<ChatModelSelector onSelectionChange={onSelectionChange} />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-V3",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: /Auto/ }));

    expect(updateSelectionMock).not.toHaveBeenCalled();
    expect(onSelectionChange).toHaveBeenLastCalledWith(
      { mode: "auto" },
      expect.objectContaining({ mode: "auto", version: 0 }),
    );
    expect(
      await screen.findByRole("button", { name: "当前模型：Auto" }),
    ).toBeInTheDocument();
  });

  it("shows the most recently resolved model beside Auto", async () => {
    const nextCatalog = catalog();
    nextCatalog.selection = {
      mode: "auto",
      provider_name: "DeepSeek",
      model_name: "DeepSeek-V3",
      availability: "available",
      version: 4,
    };
    fetchCatalogMock.mockResolvedValue(nextCatalog);

    render(<ChatModelSelector conversationId="conversation-1" />);

    expect(
      await screen.findByRole("button", {
        name: "当前模型：Auto · DeepSeek · DeepSeek-V3",
      }),
    ).toBeInTheDocument();
  });

  it("preserves an available new-chat choice when another selector loads", async () => {
    useModelSelectionStore.getState().setSelection(
      NEW_CHAT_MODEL_SELECTION_KEY,
      {
        mode: "fixed",
        model_id: "gpt-4o",
        provider_name: "OpenAI",
        model_name: "GPT-4o",
        version: 0,
      },
    );
    fetchCatalogMock.mockResolvedValue(catalog("DeepSeek-V3", 0));
    const onSelectionChange = vi.fn();

    render(<ChatModelSelector onSelectionChange={onSelectionChange} />);

    expect(
      await screen.findByRole("button", {
        name: "当前模型：OpenAI · GPT-4o",
      }),
    ).toBeInTheDocument();
    expect(onSelectionChange).toHaveBeenLastCalledWith(
      { mode: "fixed", model_id: "gpt-4o" },
      expect.objectContaining({
        model_id: "gpt-4o",
        availability: "available",
        version: 0,
      }),
    );
  });

  it("preserves a missing new-chat fixed choice as unavailable", async () => {
    useModelSelectionStore.getState().setSelection(
      NEW_CHAT_MODEL_SELECTION_KEY,
      {
        mode: "fixed",
        model_id: "removed-model",
        provider_name: "Removed Provider",
        model_name: "Removed Model",
        availability: "available",
        version: 0,
      },
    );
    fetchCatalogMock.mockResolvedValue(catalog("DeepSeek-V3", 0));
    const onSelectionChange = vi.fn();

    render(<ChatModelSelector onSelectionChange={onSelectionChange} />);

    expect(
      await screen.findByRole("button", {
        name: "当前模型：Removed Provider · Removed Model · 模型不可用",
      }),
    ).toBeInTheDocument();
    expect(
      useModelSelectionStore.getState().selections[
        NEW_CHAT_MODEL_SELECTION_KEY
      ],
    ).toMatchObject({
      mode: "fixed",
      model_id: "removed-model",
      provider_name: "Removed Provider",
      model_name: "Removed Model",
      availability: "unavailable",
    });
    expect(onSelectionChange).toHaveBeenLastCalledWith(
      { mode: "fixed", model_id: "removed-model" },
      expect.objectContaining({
        model_id: "removed-model",
        availability: "unavailable",
      }),
    );
  });

  it("preserves a disabled new-chat fixed choice as unavailable", async () => {
    useModelSelectionStore.getState().setSelection(
      NEW_CHAT_MODEL_SELECTION_KEY,
      {
        mode: "fixed",
        model_id: "gpt-4o",
        provider_name: "OpenAI",
        model_name: "GPT-4o",
        version: 0,
      },
    );
    const nextCatalog = catalog("DeepSeek-V3", 0);
    nextCatalog.providers[1].models[0].availability = "unavailable";
    fetchCatalogMock.mockResolvedValue(nextCatalog);

    render(<ChatModelSelector />);

    const trigger = await screen.findByRole("button", {
      name: "当前模型：OpenAI · GPT-4o · 模型不可用",
    });
    expect(
      useModelSelectionStore.getState().selections[
        NEW_CHAT_MODEL_SELECTION_KEY
      ],
    ).toMatchObject({
      mode: "fixed",
      model_id: "gpt-4o",
      availability: "unavailable",
    });

    fireEvent.click(trigger);
    const currentModel = within(
      screen.getByRole("dialog", { name: "对话模型选择" }),
    ).getByRole("button", { name: /GPT-4o/ });
    expect(currentModel).toBeDisabled();
    expect(currentModel).toHaveTextContent("模型不可用");
  });

  it("preserves new-chat Auto and derives availability from the catalog", async () => {
    useModelSelectionStore.getState().setSelection(
      NEW_CHAT_MODEL_SELECTION_KEY,
      { mode: "auto", availability: "available", version: 0 },
    );
    fetchCatalogMock.mockResolvedValue({
      ...catalog("DeepSeek-V3", 0),
      auto_available: false,
    });
    const onSelectionChange = vi.fn();

    render(<ChatModelSelector onSelectionChange={onSelectionChange} />);

    const trigger = await screen.findByRole("button", {
      name: "当前模型：Auto · 模型不可用",
    });
    expect(
      useModelSelectionStore.getState().selections[
        NEW_CHAT_MODEL_SELECTION_KEY
      ],
    ).toMatchObject({ mode: "auto", availability: "unavailable" });
    expect(onSelectionChange).toHaveBeenLastCalledWith(
      { mode: "auto" },
      expect.objectContaining({ mode: "auto", availability: "unavailable" }),
    );

    fireEvent.click(trigger);
    const autoOption = within(
      screen.getByRole("dialog", { name: "对话模型选择" }),
    ).getByRole("button", { name: /Auto/ });
    expect(autoOption).toBeDisabled();
    expect(autoOption).toHaveTextContent("模型不可用");
  });

  it("sends only one persisted switch when a model is clicked twice", async () => {
    let resolveUpdate!: (selection: {
      mode: "fixed";
      model_id: string;
      provider_name: string;
      model_name: string;
      version: number;
    }) => void;
    fetchCatalogMock.mockResolvedValue(catalog());
    updateSelectionMock.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveUpdate = resolve;
        }),
    );
    vi.spyOn(message, "success").mockImplementation(() => undefined as never);

    render(<ChatModelSelector conversationId="conversation-1" />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-V3",
      }),
    );
    const option = screen.getByRole("button", { name: /GPT-4o/ });
    fireEvent.click(option);
    fireEvent.click(option);

    expect(updateSelectionMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolveUpdate({
        mode: "fixed",
        model_id: "gpt-4o",
        provider_name: "OpenAI",
        model_name: "GPT-4o",
        version: 4,
      });
    });
  });

  it("keeps the previous selection when a persisted switch fails", async () => {
    fetchCatalogMock.mockResolvedValue(catalog());
    updateSelectionMock.mockRejectedValue(new Error("conflict"));
    const onSavingChange = vi.fn();

    render(
      <ChatModelSelector
        conversationId="conversation-1"
        onSavingChange={onSavingChange}
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-V3",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: /GPT-4o/ }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "模型切换失败，请重新加载后再试",
    );
    expect(
      screen.getByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-V3",
      }),
    ).toBeInTheDocument();
    expect(onSavingChange.mock.calls).toEqual([[true], [false]]);
  });

  it("releases the parent send lock when an in-flight switch unmounts", async () => {
    fetchCatalogMock.mockResolvedValue(catalog());
    updateSelectionMock.mockImplementation(() => new Promise<never>(() => undefined));
    const onSavingChange = vi.fn();

    const { unmount } = render(
      <ChatModelSelector
        conversationId="conversation-1"
        onSavingChange={onSavingChange}
      />,
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-V3",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: /GPT-4o/ }));

    await waitFor(() => expect(onSavingChange).toHaveBeenLastCalledWith(true));
    unmount();

    expect(onSavingChange.mock.calls).toEqual([[true], [false]]);
  });

  it("ignores a stale response after the conversation changes", async () => {
    let resolveFirst!: (value: ChatModelCatalog) => void;
    let resolveSecond!: (value: ChatModelCatalog) => void;
    fetchCatalogMock.mockImplementation(
      (conversationId) =>
        new Promise<ChatModelCatalog>((resolve) => {
          if (conversationId === "conversation-1") resolveFirst = resolve;
          else resolveSecond = resolve;
        }),
    );

    const { rerender } = render(
      <ChatModelSelector conversationId="conversation-1" />,
    );
    await waitFor(() =>
      expect(fetchCatalogMock).toHaveBeenCalledWith(
        "conversation-1",
        expect.any(AbortSignal),
      ),
    );
    rerender(<ChatModelSelector conversationId="conversation-2" />);

    await act(async () => {
      resolveSecond(catalog("DeepSeek-R1", 8));
    });
    expect(
      await screen.findByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-R1",
      }),
    ).toBeInTheDocument();

    await act(async () => {
      resolveFirst(catalog("旧模型", 2));
    });
    expect(
      screen.queryByRole("button", { name: "当前模型：DeepSeek · 旧模型" }),
    ).not.toBeInTheDocument();
  });

  it("opens from a matching recovery action and blocks switching while busy", async () => {
    fetchCatalogMock.mockResolvedValue(catalog());
    const warning = vi
      .spyOn(message, "warning")
      .mockImplementation(() => undefined as never);
    const { rerender } = render(
      <ChatModelSelector conversationId="conversation-1" />,
    );
    await screen.findByRole("button", {
      name: "当前模型：DeepSeek · DeepSeek-V3",
    });

    act(() => {
      window.dispatchEvent(
        new CustomEvent(CHAT_OPEN_MODEL_SELECTOR_EVENT, {
          detail: { conversationId: "other-conversation" },
        }),
      );
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    act(() => {
      window.dispatchEvent(
        new CustomEvent(CHAT_OPEN_MODEL_SELECTOR_EVENT, {
          detail: { conversationId: "conversation-1" },
        }),
      );
    });
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    expect(
      screen.getByRole("button", {
        name: "当前模型：DeepSeek · DeepSeek-V3",
      }),
    ).toHaveAttribute("aria-expanded", "false");
    rerender(
      <ChatModelSelector
        conversationId="conversation-1"
        disabled
        disabledReason="任务执行中"
      />,
    );
    act(() => {
      window.dispatchEvent(
        new CustomEvent(CHAT_OPEN_MODEL_SELECTOR_EVENT, {
          detail: { conversationId: "conversation-1" },
        }),
      );
    });
    expect(warning).toHaveBeenCalledWith("任务执行中");
    const disabledTrigger = screen.getByRole("button", {
      name: "当前模型：DeepSeek · DeepSeek-V3",
    });
    expect(disabledTrigger).toHaveAttribute("aria-disabled", "true");
    const reasonId = disabledTrigger.getAttribute("aria-describedby");
    expect(reasonId).toBeTruthy();
    expect(document.getElementById(reasonId!)).toHaveTextContent("任务执行中");
  });

  it("localizes a server-only switch block reason", async () => {
    fetchCatalogMock.mockResolvedValue({
      ...catalog(),
      switch_allowed: false,
      switch_blocked_reason: "workflow_running",
    });
    const warning = vi
      .spyOn(message, "warning")
      .mockImplementation(() => undefined as never);

    render(<ChatModelSelector conversationId="conversation-1" />);

    const trigger = await screen.findByRole("button", {
      name: "当前模型：DeepSeek · DeepSeek-V3",
    });
    expect(trigger).toHaveAttribute("aria-disabled", "true");
    const reasonId = trigger.getAttribute("aria-describedby");
    expect(reasonId).toBeTruthy();
    expect(document.getElementById(reasonId!)).toHaveTextContent(
      "Workflow 正在执行，暂时无法切换模型",
    );
    fireEvent.mouseEnter(trigger.parentElement!);
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      "Workflow 正在执行，暂时无法切换模型",
    );

    act(() => {
      window.dispatchEvent(
        new CustomEvent(CHAT_OPEN_MODEL_SELECTOR_EVENT, {
          detail: { conversationId: "conversation-1" },
        }),
      );
    });
    expect(warning).toHaveBeenCalledWith(
      "Workflow 正在执行，暂时无法切换模型",
    );
  });
});
