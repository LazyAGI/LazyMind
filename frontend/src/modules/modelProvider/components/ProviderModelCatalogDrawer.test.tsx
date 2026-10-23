import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ProviderModelCatalogDrawer } from "./ProviderModelCatalogDrawer";
import type { ProviderModel } from "../pages/ModelProvidersPage";

const mocks = vi.hoisted(() => ({ remote: vi.fn(), lookup: vi.fn() }));
vi.mock("../api", () => ({ listRemoteGroupModels: mocks.remote }));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: { name?: string }) =>
      options?.name ? `${key}:${options.name}` : key,
  }),
}));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: unknown) =>
    error instanceof Error ? error.message : String(error),
}));
vi.mock("../upstreamCatalog", () => ({ lookupUpstreamModel: mocks.lookup }));

const selected: ProviderModel[] = [
  {
    id: "private",
    name: "qwen-private",
    capability: "LLM_CHAT",
    builtIn: false,
    enabled: true,
  },
];
const remote = [
  {
    id: "deepseek-v3",
    name: "deepseek-v3",
    model_type: "llm",
    max_input_tokens: "128K",
    added: false,
  },
  {
    id: "BAAI/bge-large",
    name: "BAAI/bge-large",
    model_type: "embed",
    added: false,
  },
  {
    id: "custom/unknown",
    name: "custom/unknown",
    model_type: "",
    added: false,
  },
];

function fixture(models = selected, requiresVerification = false) {
  return {
    open: true,
    providerId: "supplier",
    providerName: "Gateway",
    protocolName: "OpenAI",
    groupId: "private-store",
    models,
    onClose: vi.fn(),
    onRefreshModels: vi.fn(),
    onAdd: vi.fn().mockResolvedValue(undefined),
    onRemove: vi.fn().mockResolvedValue(true),
    onChanged: vi.fn(),
    requiresVerification,
    onCheckConnection: vi.fn(),
  };
}
function draw(props: ReturnType<typeof fixture>) {
  return render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <ProviderModelCatalogDrawer {...props} />
    </ConfigProvider>,
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.remote.mockResolvedValue({ models: remote });
  mocks.lookup.mockReturnValue(undefined);
});

describe("provider model catalog drawer", () => {
  it("uses one bulk callback for multi-model additions and keeps partial failures retryable", async () => {
    let finish!: (result: { succeeded: number; failed: number }) => void;
    const props = { ...fixture([]), onAddMany: vi.fn().mockImplementation(() => new Promise((resolve) => { finish = resolve; })) };
    draw(props);
    await screen.findByText("deepseek-v3");
    const add = screen.getByRole("button", { name: "modelProvider.catalog.addAll" });
    fireEvent.click(add);
    fireEvent.click(add);
    await waitFor(() => expect(props.onAddMany).toHaveBeenCalledTimes(1));
    expect(props.onAddMany).toHaveBeenCalledWith([
      { name: "deepseek-v3", model_type: "llm", max_input_tokens: "128K", vision: false },
      { name: "BAAI/bge-large", model_type: "embed", max_input_tokens: undefined, vision: false },
      { name: "custom/unknown", model_type: "", max_input_tokens: undefined, vision: false },
    ]);
    expect(props.onAdd).not.toHaveBeenCalled();
    expect(add).toBeDisabled();
    await act(async () => { finish({ succeeded: 2, failed: 1 }); });
    expect(await screen.findByText("modelProvider.catalog.partialFailure")).toBeInTheDocument();
    await waitFor(() => expect(add).toBeEnabled());
    expect(props.onChanged).toHaveBeenCalledTimes(1);
  });

  it("places bulk add before bulk removal and removes all saved models across filters without confirmation", async () => {
    const saved = [selected[0], { ...selected[0], id: "remote-saved", name: "deepseek-v3", builtIn: true }];
    const props = fixture(saved);
    draw(props);
    await screen.findByText("deepseek-v3");
    const add = screen.getByRole("button", { name: "modelProvider.catalog.addAll" });
    const remove = screen.getByRole("button", { name: "modelProvider.catalog.removeAll" });
    expect(add.compareDocumentPosition(remove) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.catalog.search" }), { target: { value: "BAAI" } });
    fireEvent.click(remove);
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledTimes(2));
    expect(props.onRemove.mock.calls.map(([model]) => model.name).sort()).toEqual(["deepseek-v3", "qwen-private"]);
    expect(screen.queryByRole("button", { name: "common.delete" })).not.toBeInTheDocument();
  });

  it("shows unsupported OpenRouter types without allowing them to be imported", async () => {
    mocks.remote.mockResolvedValueOnce({ models: [
      { id: "text-vector", name: "text-vector", model_type: "embed" },
      { id: "image", name: "image", model_type: "text2image" },
      { id: "reranker", name: "reranker", model_type: "rerank" },
      { id: "image-edit", name: "image-edit", model_type: "image_editing" },
      { id: "joint-vector", name: "joint-vector", model_type: "multimodal_embedding" },
    ] });
    const props = { ...fixture([]), protocolName: "OpenRouter" };
    draw(props);
    await screen.findByText("joint-vector");
    for (const name of ["reranker", "image-edit", "joint-vector"])
      expect(screen.getByRole("button", { name: `modelProvider.catalog.addModel:${name}` })).toBeDisabled();
    expect(screen.getAllByText("modelProvider.catalog.unsupportedType")).toHaveLength(3);
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.addAll" }));
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledTimes(2));
    expect(props.onAdd.mock.calls.map(([item]) => item.model_type)).toEqual(["text2image", "embed"]);
  });
  it("blocks incompatible remote types and bulk imports for the current protocol", async () => {
    const props = { ...fixture([]), protocolName: "Claude" };
    draw(props);
    await screen.findByText("BAAI/bge-large");
    expect(screen.getByRole("button", { name: "modelProvider.catalog.addModel:BAAI/bge-large" })).toBeDisabled();
    expect(screen.getByText("modelProvider.catalog.unsupportedType")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.addAll" }));
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledTimes(2));
    expect(props.onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "deepseek-v3", model_type: "llm" }));
    expect(props.onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "custom/unknown", model_type: "" }));
    expect(props.onAdd).not.toHaveBeenCalledWith(expect.objectContaining({ name: "BAAI/bge-large" }));
  });
  it("shows only populated task tabs, retains counts while searching, and keeps pinned models free of custom labels", async () => {
    const { baseElement } = draw(fixture());
    await screen.findByText("deepseek-v3");
    const tabs = screen.getAllByRole("tab");
    expect(tabs).toHaveLength(5);
    expect(screen.queryByRole("tab", { name: /modelProvider.taskType.rerank/ })).not.toBeInTheDocument();
    const embedding = screen.getByRole("tab", { name: /modelProvider.taskType.embedding/ });
    expect(embedding).toHaveTextContent("1");
    expect(screen.getAllByRole("region", { name: "modelProvider.taskType.chat" }).some((region) => region.textContent?.includes("qwen-private"))).toBe(true);
    expect(screen.queryByText("modelProvider.catalog.customGroup")).not.toBeInTheDocument();
    expect(baseElement.querySelector(".provider-model-catalog-row .provider-model-catalog-model-name")).toHaveTextContent("qwen-private");
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.catalog.search" }), { target: { value: "deepseek" } });
    expect(screen.getAllByRole("tab")).toHaveLength(5);
    expect(embedding).toHaveTextContent("1");
    expect(screen.queryByText("BAAI/bge-large")).not.toBeInTheDocument();
  });
  it("supports arrow, Home and End navigation with one tab stop for task filters", async () => {
    draw(fixture());
    await screen.findByText("deepseek-v3");
    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveAttribute("tabindex", "0");
    expect(tabs.slice(1).every((tab) => tab.getAttribute("tabindex") === "-1")).toBe(true);
    fireEvent.keyDown(tabs[0], { key: "ArrowRight" });
    expect(tabs[1]).toHaveFocus();
    expect(tabs[1]).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(tabs[1], { key: "End" });
    expect(tabs[tabs.length - 1]).toHaveFocus();
    fireEvent.keyDown(tabs[tabs.length - 1], { key: "Home" });
    expect(tabs[0]).toHaveFocus();
    expect(screen.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", tabs[0].id);
  });
  it("discloses the complete model ID on a tap without adding that model", async () => {
    const props = fixture([]);
    draw(props);
    const name = await screen.findByRole("button", { name: "deepseek-v3" });
    fireEvent.click(name);
    expect(await screen.findByRole("tooltip")).toHaveTextContent("deepseek-v3");
    expect(props.onAdd).not.toHaveBeenCalled();
  });
  it("returns to all models after the user explicitly classifies an existing unknown model", async () => {
    mocks.remote.mockResolvedValueOnce({ models: [remote[2]] });
    const props = fixture([]);
    const view = draw(props);
    fireEvent.click(await screen.findByRole("tab", { name: /modelProvider.taskType.unknown/ }));
    expect(screen.getByRole("button", { name: "modelProvider.catalog.addModel:custom/unknown" })).toBeEnabled();
    expect(props.onAdd).not.toHaveBeenCalled();
    view.rerender(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <ProviderModelCatalogDrawer {...props} models={[{
          id: "added",
          name: "custom/unknown",
          capability: "LLM_CHAT",
          builtIn: false,
          enabled: true,
        }]} />
      </ConfigProvider>,
    );
    await waitFor(() => expect(screen.queryByRole("tab", { name: /modelProvider.taskType.unknown/ })).not.toBeInTheDocument());
    expect(screen.getByRole("tab", { name: /modelProvider.catalog.all/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: /modelProvider.taskType.chat/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "modelProvider.catalog.removeModel:custom/unknown" })).toBeEnabled();
  });
  it("uses model identities for mixed-brand models without borrowing the supplier protocol logo", async () => {
    mocks.remote.mockResolvedValueOnce({
      models: [
        {
          id: "hy4-preview",
          name: "hy4-preview",
          model_type: "llm",
          added: false,
        },
        {
          id: "unknown-model",
          name: "unknown-model",
          model_type: "llm",
          added: false,
        },
      ],
    });
    draw(fixture([]));
    const known = await screen.findByText("hy4-preview");
    expect(
      known.closest(".provider-model-catalog-row")?.querySelector("img"),
    ).toHaveAttribute("src", "/provider-icons/hunyuan.svg");
    expect(
      screen.getByRole("region", { name: "modelProvider.taskType.chat" }),
    ).toBeInTheDocument();
    const unknown = screen.getByText("unknown-model");
    expect(
      unknown.closest(".provider-model-catalog-row")?.querySelector("img"),
    ).toHaveAttribute("src", "/provider-icons/custom.svg");
  });

  it("provides named collapsible task controls without announcing their decorative icons", async () => {
    draw(fixture([]));
    await screen.findByText("deepseek-v3");
    const toggle = screen.getByRole("button", {
      name: "modelProvider.taskType.chat",
    });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("deepseek-v3")).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(screen.getByText("deepseek-v3")).toBeInTheDocument();
  });

  it("shows the discovered non-text model category even without upstream modality metadata", async () => {
    draw(fixture([]));
    const name = await screen.findByText("BAAI/bge-large");
    const row = name.closest(".provider-model-catalog-row") as HTMLElement;
    expect(
      within(row).getByText("modelProvider.taskType.embedding", {
        exact: true,
      }),
    ).toBeInTheDocument();
  });
  it("clears the loading overlay when the optional local loading flag is absent", async () => {
    draw(fixture());
    await screen.findByText("deepseek-v3");
    await waitFor(() =>
      expect(
        document.querySelector(
          ".provider-model-catalog-drawer .ant-spin-spinning",
        ),
      ).not.toBeInTheDocument(),
    );
  });
  it("asks for an explicit connection check before discovery when the saved key is unverified", async () => {
    const props = fixture(selected, true);
    draw(props);
    expect(
      await screen.findByText("modelProvider.catalog.verifyBeforeSync"),
    ).toBeInTheDocument();
    expect(mocks.remote).not.toHaveBeenCalled();
    expect(screen.getByText("qwen-private")).toBeInTheDocument();
    expect(
      screen.queryByText("modelProvider.catalog.missingRemote"),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.workspace.checkConnection",
      }),
    );
    expect(props.onCheckConnection).toHaveBeenCalledTimes(1);
    expect(mocks.remote).not.toHaveBeenCalled();
  });

  it("loads the remote list without importing it and pins existing custom models", async () => {
    const props = fixture();
    const { baseElement } = draw(props);
    await screen.findByText("deepseek-v3");
    expect(mocks.remote).toHaveBeenCalledWith("supplier", "private-store");
    expect(props.onAdd).not.toHaveBeenCalled();
    const rows = baseElement.querySelectorAll(".provider-model-catalog-row");
    expect(rows[0]).toHaveTextContent("qwen-private");
    expect(rows[0]).toHaveClass("is-added");
    expect(rows[0]).toHaveTextContent("modelProvider.catalog.missingRemote");
  });

  it("adds only the clicked model with its discovered type and context window", async () => {
    const props = fixture();
    draw(props);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "modelProvider.catalog.addModel:deepseek-v3",
      }),
    );
    await waitFor(() =>
      expect(props.onAdd).toHaveBeenCalledWith(
        expect.objectContaining({
          name: "deepseek-v3",
          model_type: "llm",
          max_input_tokens: "128K",
        }),
      ),
    );
    expect(props.onAdd).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(props.onChanged).toHaveBeenCalledTimes(1));
  });

  it("searches vendor and model names and filters embeddings", async () => {
    draw(fixture());
    await screen.findByText("deepseek-v3");
    fireEvent.click(
      screen.getByRole("tab", { name: /modelProvider.taskType.embedding/ }),
    );
    expect(screen.getByText("BAAI/bge-large")).toBeInTheDocument();
    expect(screen.queryByText("deepseek-v3")).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("tab", { name: /modelProvider.catalog.all/ }),
    );
    fireEvent.change(
      screen.getByRole("textbox", { name: "modelProvider.catalog.search" }),
      { target: { value: "deepseek" } },
    );
    expect(screen.getByText("deepseek-v3")).toBeInTheDocument();
    expect(screen.queryByText("BAAI/bge-large")).not.toBeInTheDocument();
  });

  it("removes a multimodal vector directly without a confirmation", async () => {
    const props = fixture([
      {
        id: "multimodal",
        name: "multi-vector",
        capability: "MULTIMODAL_EMBEDDING",
        builtIn: false,
        enabled: true,
      },
    ]);
    draw(props);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "modelProvider.catalog.removeModel:multi-vector",
      }),
    );
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledWith(props.models[0]));
    expect(
      screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("modelProvider.catalog.removeDescription")).not.toBeInTheDocument();
  });

  it("removes an embedding directly and retains it after a failed request", async () => {
    const embedding: ProviderModel = {
      id: "embedding",
      name: "BAAI/bge-large",
      capability: "EMBEDDING",
      builtIn: false,
      enabled: true,
    };
    const props = fixture([embedding]);
    let resolveRemoval!: (value: boolean) => void;
    props.onRemove.mockReturnValueOnce(new Promise<boolean>((resolve) => { resolveRemoval = resolve; }));
    draw(props);
    const remove = await screen.findByRole("button", { name: "modelProvider.catalog.removeModel:BAAI/bge-large" });
    act(() => { fireEvent.click(remove); fireEvent.click(remove); });
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledWith(embedding));
    expect(props.onRemove).toHaveBeenCalledTimes(1);
    expect(remove).toBeDisabled();
    expect(screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc")).not.toBeInTheDocument();
    await act(async () => resolveRemoval(false));
    expect(await screen.findByText("modelProvider.catalog.partialFailure")).toBeInTheDocument();
    expect(props.onChanged).not.toHaveBeenCalled();
    expect(screen.getByText(embedding.name)).toBeVisible();
    await waitFor(() => expect(remove).toBeEnabled());
    fireEvent.click(remove);
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledTimes(2));
  });

  it("removes selected task groups and unavailable models without confirmation", async () => {
    mocks.remote.mockResolvedValueOnce({ models: remote.slice(1) });
    const props = fixture();
    draw(props);
    await screen.findByText("BAAI/bge-large");
    const group = await screen.findByRole("button", { name: /modelProvider.catalog.removeGroup/ });
    fireEvent.click(group);
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledTimes(1));
    expect(props.onRemove).toHaveBeenCalledWith(selected[0]);
    expect(screen.queryByRole("button", { name: "common.delete" })).not.toBeInTheDocument();
    await waitFor(() => expect(group).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.removeAll" }));
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledTimes(2));
    expect(props.onRemove).toHaveBeenLastCalledWith(selected[0]);
  });

  it("allows removing saved models without mislabeling them when discovery fails", async () => {
    mocks.remote.mockRejectedValue(new Error("discovery offline"));
    draw(fixture());
    expect(await screen.findByText("discovery offline")).toBeInTheDocument();
    expect(
      screen.queryByText("modelProvider.catalog.missingRemote"),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "modelProvider.catalog.removeAll",
      }),
    ).toBeEnabled();
  });

  it("adds missing models including unclassified entries without reimporting saved models", async () => {
    const props = fixture([
      {
        id: "deepseek",
        name: "deepseek-v3",
        capability: "LLM_CHAT",
        builtIn: true,
        enabled: true,
      },
    ]);
    draw(props);
    await screen.findByText("BAAI/bge-large");
    fireEvent.click(
      screen.getByRole("button", { name: "modelProvider.catalog.addAll" }),
    );
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledTimes(2));
    expect(props.onAdd.mock.calls.map(([model]) => model.name)).toEqual([
      "BAAI/bge-large",
      "custom/unknown",
    ]);
    expect(props.onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "custom/unknown", model_type: "" }));
    expect(props.onRemove).not.toHaveBeenCalled();
  });

  it("adds an unclassified model without requiring a type picker", async () => {
    const props = fixture();
    draw(props);
    const add = await screen.findByRole("button", {
      name: "modelProvider.catalog.addModel:custom/unknown",
    });
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    expect(add).toBeEnabled();
    fireEvent.click(add);
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "custom/unknown", model_type: "" })));
    expect(screen.queryByText("modelProvider.catalog.unclassifiedType")).not.toBeInTheDocument();
    expect(add).not.toHaveAttribute("title");
  });

  it("adds an unclassified group while retaining exact remote context", async () => {
    mocks.remote.mockResolvedValueOnce({ models: [remote[2], {
      id: "another-unknown", name: "another-unknown", model_type: "", added: false, max_input_tokens: "262144",
    }] });
    const props = fixture([]);
    draw(props);
    const add = await screen.findByRole("button", {
      name: "modelProvider.catalog.addGroup:modelProvider.taskType.unknown",
    });
    expect(add).toBeEnabled();
    fireEvent.click(add);
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledTimes(2));
    expect(props.onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "another-unknown", model_type: "", max_input_tokens: "262144" }));
  });

  it("keeps distinct task groups for rerank, embedding, speech recognition and speech synthesis", async () => {
    mocks.remote.mockResolvedValueOnce({
      models: [
        { id: "embedding", name: "embedding", model_type: "embed" },
        { id: "reranker", name: "reranker", model_type: "rerank" },
        { id: "recognize", name: "recognize", model_type: "stt" },
        { id: "synthesize", name: "synthesize", model_type: "tts" },
        { id: "vision-chat", name: "vision-chat", model_type: "vlm" },
      ],
    });
    draw(fixture([]));
    await screen.findByText("reranker");
    for (const type of ["embedding", "rerank", "asr", "tts", "chat"]) {
      expect(
        screen.getByRole("region", { name: `modelProvider.taskType.${type}` }),
      ).toBeInTheDocument();
    }
    fireEvent.click(
      screen.getByRole("tab", { name: /modelProvider.taskType.rerank/ }),
    );
    expect(screen.getByText("reranker")).toBeInTheDocument();
    expect(screen.queryByText("embedding")).not.toBeInTheDocument();
    expect(screen.queryByText("vision-chat")).not.toBeInTheDocument();
  });

  it("uses saved task types ahead of remote metadata and pins custom models within each task", async () => {
    const models: ProviderModel[] = [
      {
        id: "built",
        name: "gpt-built",
        capability: "LLM_CHAT",
        builtIn: true,
        enabled: true,
      },
      {
        id: "custom",
        name: "gpt-custom",
        capability: "LLM_CHAT",
        builtIn: false,
        enabled: true,
      },
      {
        id: "rerank",
        name: "custom-rerank",
        capability: "RERANK",
        builtIn: false,
        enabled: true,
      },
    ];
    mocks.remote.mockResolvedValueOnce({
      models: [
        { id: "built", name: "gpt-built", model_type: "llm" },
        { id: "custom", name: "gpt-custom", model_type: "text2image" },
        { id: "rerank", name: "custom-rerank", model_type: "embed" },
      ],
    });
    draw(fixture(models));
    await screen.findByText("gpt-custom");
    const rows = [...document.querySelectorAll(".provider-model-catalog-row")];
    expect(
      rows.map(
        (row) =>
          row.querySelector(".provider-model-catalog-model-name")?.textContent,
      ),
    ).toEqual(["gpt-custom", "gpt-built", "custom-rerank"]);
    expect(
      within(rows[0] as HTMLElement).getByText("modelProvider.taskType.chat"),
    ).toBeInTheDocument();
    expect(
      within(rows[2] as HTMLElement).getByText("modelProvider.taskType.rerank"),
    ).toBeInTheDocument();
  });

  it("orders tabs and sections like the main task bar while keeping custom unknowns last", async () => {
    mocks.remote.mockResolvedValueOnce({ models: [
      { id: "tts", name: "tts", model_type: "tts" },
      { id: "unknown", name: "unknown", model_type: "" },
      { id: "image-edit", name: "image-edit", model_type: "image_editing" },
      { id: "embed", name: "embed", model_type: "embed" },
      { id: "chat", name: "chat", model_type: "llm" },
      { id: "image", name: "image", model_type: "text2image" },
      { id: "video", name: "video", model_type: "text2video" },
      { id: "stt", name: "stt", model_type: "stt" },
      { id: "rerank", name: "rerank", model_type: "rerank" },
    ] });
    draw(fixture([{ id: "saved-unknown", name: "unknown", capability: "UNKNOWN", builtIn: false, enabled: true }]));
    await screen.findByText("rerank");
    const order = ["chat", "image-generation", "image-edit", "video-generation", "embedding", "rerank", "tts", "asr", "unknown"];
    expect(screen.getAllByRole("region").map((region) => region.getAttribute("aria-label"))).toEqual(order.map((task) => `modelProvider.taskType.${task}`));
    expect(screen.getAllByRole("tab").slice(2).map((tab) => tab.textContent?.replace(/\d+$/, ""))).toEqual(order.map((task) => `modelProvider.taskType.${task}`));
    expect(document.querySelectorAll(".provider-model-catalog-row")[8]).toHaveTextContent("unknown");
  });

  it("adds missing and future task types as unclassified without guessing chat from modalities", async () => {
    mocks.remote.mockResolvedValueOnce({
      models: [
        { id: "omni", name: "omni", model_type: "", output: ["text", "audio"] },
        { id: "unsupported", name: "unsupported", model_type: "audio" },
      ],
    });
    mocks.lookup.mockReturnValue({
      name: "omni",
      model_type: "llm",
      input: ["image", "audio"],
      output: ["text", "audio"],
      context: 128000,
      max_output: 4096,
    });
    const props = fixture([]);
    draw(props);
    await screen.findByText("omni");
    expect(
      screen.getByRole("button", {
        name: "modelProvider.catalog.addModel:omni",
      }),
    ).toBeEnabled();
    expect(
      screen.getByRole("button", {
        name: "modelProvider.catalog.addModel:unsupported",
      }),
    ).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.addAll" }));
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledTimes(2));
    expect(props.onAdd.mock.calls.map(([item]) => item.model_type)).toEqual(["", ""]);
  });

  it("fills an ID-only remote model from the catalog and passes its exact type when clicked", async () => {
    mocks.remote.mockResolvedValueOnce({ models: [{ id: "gateway/vector-v2", name: "gateway/vector-v2" }] });
    mocks.lookup.mockReturnValue({ name: "vendor/vector-v2", model_type: "embed", input: ["text"], output: ["embeddings"], context: 1000, max_output: 0 });
    const props = fixture([]);
    draw(props);
    const add = await screen.findByRole("button", { name: "modelProvider.catalog.addModel:gateway/vector-v2" });
    expect(add).toBeEnabled();
    fireEvent.click(add);
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "gateway/vector-v2", model_type: "embed" })));
  });

  it("does not overwrite unknown outputs or explicit future types with catalog recognition", async () => {
    mocks.remote.mockResolvedValueOnce({ models: [
      { id: "raw-unknown", name: "raw-unknown", model_type: "", output: ["decisions"] },
      { id: "declared-unknown", name: "declared-unknown", model_type: "unknown" },
      { id: "future", name: "future", model_type: "future-task", output: [] },
    ] });
    mocks.lookup.mockReturnValue({ name: "known", model_type: "llm", input: ["text"], output: ["text"], context: 1000, max_output: 0 });
    const props = fixture([]);
    draw(props);
    await screen.findByText("raw-unknown");
    for (const name of ["raw-unknown", "declared-unknown", "future"])
      expect(screen.getByRole("button", { name: `modelProvider.catalog.addModel:${name}` })).toBeEnabled();
    expect(screen.queryByText("modelProvider.catalog.unclassifiedType")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.addAll" }));
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledTimes(3));
    expect(props.onAdd.mock.calls.map(([item]) => item.model_type)).toEqual(["", "", ""]);
  });

  it("retains a saved manual type when both discovery and recognition disagree", async () => {
    mocks.remote.mockResolvedValueOnce({ models: [{ id: "manual", name: "manual", model_type: "embed", output: ["embeddings"] }] });
    mocks.lookup.mockReturnValue({ name: "manual", model_type: "tts", input: ["text"], output: ["speech"], context: 0, max_output: 0 });
    const props = fixture([{ id: "saved", name: "manual", capability: "LLM_CHAT", builtIn: false, enabled: true }]);
    draw(props);
    await screen.findByText("manual");
    const row = screen.getByRole("button", { name: "modelProvider.catalog.removeModel:manual" }).closest(".provider-model-catalog-row") as HTMLElement;
    expect(within(row).getByText("modelProvider.taskType.chat")).toBeInTheDocument();
    expect(within(row).queryByText("modelProvider.taskType.embedding")).not.toBeInTheDocument();
  });

  it("ignores a stale discovery response after switching suppliers", async () => {
    let resolveFirst: (value: unknown) => void = () => undefined;
    mocks.remote
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveFirst = resolve;
        }),
      )
      .mockResolvedValueOnce({ models: [remote[1]] });
    const props = fixture();
    const view = draw(props);
    view.rerender(
      <ConfigProvider theme={{ token: { motion: false } }}>
        <ProviderModelCatalogDrawer {...props} providerId="next-supplier" />
      </ConfigProvider>,
    );
    await screen.findByText("BAAI/bge-large");
    await act(async () => resolveFirst({ models: [remote[0]] }));
    expect(screen.queryByText("deepseek-v3")).not.toBeInTheDocument();
  });

  it("keeps the drawer open until an in-flight model change completes", async () => {
    let finish: () => void = () => undefined;
    const props = fixture();
    props.onAdd.mockReturnValue(
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
    );
    const view = draw(props);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "modelProvider.catalog.addModel:deepseek-v3",
      }),
    );
    expect(
      screen.queryByRole("button", { name: "Close" }),
    ).not.toBeInTheDocument();
    const mask = view.baseElement.querySelector(".ant-drawer-mask");
    if (mask) fireEvent.click(mask);
    expect(props.onClose).not.toHaveBeenCalled();
    await act(async () => finish());
    fireEvent.click(await screen.findByRole("button", { name: "Close" }));
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });
});
