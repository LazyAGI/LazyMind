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

  it("confirms a multimodal vector removal without claiming a text-index downgrade", async () => {
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
    expect(
      await screen.findByText("modelProvider.catalog.removeDescription"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc"),
    ).not.toBeInTheDocument();
    expect(props.onRemove).not.toHaveBeenCalled();
  });

  it("asks before removing an embedding model and preserves it when canceled", async () => {
    const embedding: ProviderModel = {
      id: "embedding",
      name: "BAAI/bge-large",
      capability: "EMBEDDING",
      builtIn: false,
      enabled: true,
    };
    const props = fixture([embedding]);
    draw(props);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "modelProvider.catalog.removeModel:BAAI/bge-large",
      }),
    );
    expect(
      await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc"),
    ).toBeInTheDocument();
    expect(props.onRemove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    await waitFor(() =>
      expect(
        screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc"),
      ).not.toBeInTheDocument(),
    );
    expect(props.onRemove).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.catalog.removeModel:BAAI/bge-large",
      }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "common.delete" }),
    );
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledWith(embedding));
  });

  it("does not mark saved models unlisted when discovery fails", async () => {
    mocks.remote.mockRejectedValue(new Error("discovery offline"));
    draw(fixture());
    expect(await screen.findByText("discovery offline")).toBeInTheDocument();
    expect(
      screen.queryByText("modelProvider.catalog.missingRemote"),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "modelProvider.catalog.clearUnavailable",
      }),
    ).toBeDisabled();
  });

  it("adds only explicitly typed missing models and leaves unknown models unselected", async () => {
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
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledTimes(1));
    expect(props.onAdd.mock.calls.map(([model]) => model.name)).toEqual([
      "BAAI/bge-large",
    ]);
    expect(props.onRemove).not.toHaveBeenCalled();
  });

  it("lets the user specify unknown model types before adding", async () => {
    const props = fixture();
    draw(props);
    const type = await screen.findByRole("combobox", {
      name: "modelProvider.catalog.modelType:custom/unknown",
    });
    expect(
      screen.getByRole("button", {
        name: "modelProvider.catalog.addModel:custom/unknown",
      }),
    ).toBeDisabled();
    fireEvent.mouseDown(type);
    await screen.findByRole("listbox");
    fireEvent.click(
      screen.getByText("modelProvider.capability.embedding", {
        selector: ".ant-select-item-option-content",
      }),
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.catalog.addModel:custom/unknown",
      }),
    );
    await waitFor(() =>
      expect(props.onAdd).toHaveBeenCalledWith(
        expect.objectContaining({
          name: "custom/unknown",
          model_type: "embed",
        }),
      ),
    );
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

  it("uses saved task types ahead of remote metadata and keeps all custom models above built-ins", async () => {
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
    ).toEqual(["gpt-custom", "custom-rerank", "gpt-built"]);
    expect(
      within(rows[0] as HTMLElement).getByText("modelProvider.taskType.chat"),
    ).toBeInTheDocument();
    expect(
      within(rows[1] as HTMLElement).getByText("modelProvider.taskType.rerank"),
    ).toBeInTheDocument();
  });

  it("does not infer a runnable task only from input and output modalities", async () => {
    mocks.remote.mockResolvedValueOnce({
      models: [
        { id: "omni", name: "omni", model_type: "" },
        { id: "unsupported", name: "unsupported", model_type: "audio" },
      ],
    });
    mocks.lookup.mockReturnValue({
      name: "omni",
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
    ).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "modelProvider.catalog.addModel:unsupported",
      }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "modelProvider.catalog.addAll" }),
    ).toBeDisabled();
    expect(props.onAdd).not.toHaveBeenCalled();
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
