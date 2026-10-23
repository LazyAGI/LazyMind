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
import type { ProviderModel } from "../pages/ModelProvidersPage";
import {
  SavedProviderModels,
  type SavedProviderModelsProps,
} from "./SavedProviderModels";

const upstream = vi.hoisted(() => ({ lookup: vi.fn() }));
vi.mock("../upstreamCatalog", () => ({ lookupUpstreamModel: upstream.lookup }));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: unknown) => error instanceof Error ? error.message : String(error),
}));
beforeEach(() => upstream.lookup.mockReturnValue(undefined));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: { name?: string }) =>
      values?.name ? `${key}:${values.name}` : key,
  }),
}));
const models: ProviderModel[] = [
  {
    id: "catalog",
    name: "deepseek-catalog",
    capability: "LLM_CHAT",
    builtIn: true,
    enabled: true,
    maxInputTokens: "128K",
  },
  {
    id: "custom",
    name: "gpt-4o",
    capability: "LLM_CHAT",
    builtIn: false,
    enabled: true,
    maxInputTokens: "1M",
  },
  {
    id: "embedding",
    name: "BAAI/bge-large",
    capability: "EMBEDDING",
    builtIn: false,
    enabled: true,
  },
];
function setup(overrides: Partial<SavedProviderModelsProps> = {}) {
  const props: SavedProviderModelsProps = {
    models,
    loading: false,
    providerName: "Doubao",
    supplierName: "Gateway",
    capabilityLabel: (capability) => capability,
    onEdit: vi.fn(),
    onRemove: vi.fn(),
    ...overrides,
  };
  const view = render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <SavedProviderModels {...props} />
    </ConfigProvider>,
  );
  return { ...view, props };
}
function groupLabels(container: HTMLElement) {
  return [...container.querySelectorAll(".saved-provider-model-group")].map(
    (group) => group.getAttribute("aria-label"),
  );
}

describe("saved supplier models", () => {
  it("honors disabled visual input instead of reviving the catalog default", async () => {
    upstream.lookup.mockReturnValue({ input: ["text", "image"], output: ["text"] });
    setup({ providerName: "OpenAI", models: [{ ...models[1], vision: false }] });
    fireEvent.click(screen.getByRole("button", { name: "gpt-4o" }));
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).not.toHaveTextContent("modelProvider.visionSupported");
  });
  it("shows a saved visual dialogue context window and exposes its model settings", async () => {
    setup({
      models: [
        {
          ...models[1],
          capability: "VLM",
          name: "private-vlm",
          maxInputTokens: "200000",
        },
      ],
    });
    fireEvent.click(
      screen.getByRole("button", { name: "private-vlm" }),
    );
    const context = await screen.findByText(
      "modelProvider.workspace.context: 200000",
    );
    await waitFor(() => expect(context).toBeVisible());
    expect(
      screen.queryByRole("button", {
        name: "modelProvider.editModelAria:private-vlm",
      }),
    ).toBeInTheDocument();
  });
  it("keeps custom models above built-ins in one flat list without collapsible task headings", () => {
    const customChat = { ...models[1], name: "gpt-4o-custom" };
    const builtInChat = { ...models[0], name: "gpt-4o-builtin" };
    const { container } = setup({
      models: [builtInChat, customChat, models[2]],
    });
    expect(
      screen
        .getAllByRole("listitem")
        .map((row) => row.getAttribute("aria-label")),
    ).toEqual(["gpt-4o-custom", "BAAI/bge-large", "gpt-4o-builtin"]);
    expect(groupLabels(container)).toEqual([]);
    expect(screen.getAllByRole("list")).toHaveLength(1);
    expect(container.querySelector("[aria-expanded]")).toBeNull();
  });
  it("flattens mixed tasks while preserving model logos, types and settings", () => {
    const vlm: ProviderModel = {
      ...models[1],
      id: "vlm",
      name: "private-vlm",
      capability: "VLM",
    };
    const evolution: ProviderModel = {
      ...models[1],
      id: "evolution",
      name: "private-evolution",
      capability: "LLM_SELF_EVOLUTION",
    };
    const rerank: ProviderModel = {
      ...models[2],
      id: "rerank",
      name: "BAAI/reranker",
      capability: "RERANK",
    };
    const { container } = setup({
      models: [models[1], vlm, evolution, models[2], rerank],
    });
    expect(groupLabels(container)).toEqual([]);
    expect(screen.getAllByRole("listitem")).toHaveLength(5);
    const gpt = screen.getByRole("listitem", { name: "gpt-4o" });
    expect(gpt.querySelector("img")).toHaveAttribute(
      "src",
      "/provider-icons/openai.svg",
    );
    expect(gpt.querySelector("img")).toHaveAttribute("alt", "");
    const vlmRow = screen.getByRole("listitem", { name: vlm.name });
    expect(vlmRow.querySelector("img")).toHaveAttribute(
      "src",
      "/provider-icons/custom.svg",
    );
    expect(
      within(vlmRow).queryByRole("button", {
        name: `modelProvider.editModelAria:${vlm.name}`,
      }),
    ).toBeInTheDocument();
    expect(container.querySelector("[role=table]")).toBeNull();
  });
  it("opens model settings for every task and built-in model without rewriting its capability", () => {
    const editable: ProviderModel[] = [
      { ...models[0], id: "builtin-chat", name: "builtin-chat" },
      ...(["VLM", "EMBEDDING", "MULTIMODAL_EMBEDDING", "RERANK", "ASR", "TTS", "TEXT_TO_IMAGE", "IMAGE_EDITING", "TEXT_TO_VIDEO"] as const).map((capability) => ({
        id: capability, name: capability, capability, builtIn: false, enabled: true,
      })),
    ];
    const { props } = setup({ models: editable });
    for (const model of editable) {
      fireEvent.click(screen.getByRole("button", { name: `modelProvider.editModelAria:${model.name}` }));
      expect(props.onEdit).toHaveBeenLastCalledWith(model);
    }
    expect(props.onEdit).toHaveBeenCalledTimes(editable.length);
  });
  it("uses the shared colored task image badge for every saved task category", () => {
    const taskModels: ProviderModel[] = [
      ["LLM_CHAT", "chat"],
      ["EMBEDDING", "embedding"],
      ["RERANK", "rerank"],
      ["ASR", "asr"],
      ["TTS", "tts"],
      ["TEXT_TO_IMAGE", "image-generation"],
      ["IMAGE_EDITING", "image-edit"],
      ["TEXT_TO_VIDEO", "video-generation"],
    ].map(([capability, name]) => ({
      id: name,
      name,
      capability: capability as ProviderModel["capability"],
      builtIn: false,
      enabled: true,
    }));
    setup({ models: taskModels });
    for (const model of taskModels) {
      expect(
        screen
          .getByRole("listitem", { name: model.name })
          .querySelector(".model-type-badge img"),
      ).toHaveAttribute("src", `/model-type-icons/${model.name}.svg`);
    }
  });
  it("makes configured vision and context discoverable through model details without extra row badges", async () => {
    const vision: ProviderModel = {
      ...models[1],
      name: "private-vision-model",
      vision: true,
    };
    const { container } = setup({ models: [vision] });
    const row = screen.getByRole("listitem", { name: vision.name });
    expect(row).not.toHaveTextContent("1M");
    expect(
      container.querySelector(
        ".model-modality-marks, .saved-provider-model-vision, .saved-provider-model-context",
      ),
    ).toBeNull();
    fireEvent.click(within(row).getByRole("button", { name: vision.name }));
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent(vision.name);
    expect(tooltip).toHaveTextContent("LLM_CHAT");
    expect(tooltip).toHaveTextContent("modelProvider.visionSupported");
    expect(tooltip).toHaveTextContent("modelProvider.workspace.context: 1M");
  });
  it("explains a task badge's actual purpose on hover", async () => {
    setup({ models: [models[2]] });
    fireEvent.mouseEnter(
      screen
        .getByRole("listitem", { name: models[2].name })
        .querySelector(".model-type-badge")!,
    );
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      "modelProvider.taskTypeDescription.embedding",
    );
  });
  it.each(["EMBEDDING", "MULTIMODAL_EMBEDDING"] as const)(
    "removes %s directly without a confirmation",
    async (capability) => {
      const embedding: ProviderModel = { ...models[2], capability };
      const { props } = setup({ models: [embedding] });
      fireEvent.click(
        screen.getByRole("button", {
          name: `modelProvider.deleteModelAria:${embedding.name}`,
        }),
      );
      await waitFor(() =>
        expect(props.onRemove).toHaveBeenCalledWith(embedding),
      );
      expect(props.onRemove).toHaveBeenCalledTimes(1);
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      expect(screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc")).not.toBeInTheDocument();
    },
  );
  it("retains VLM's underlying capability when removing directly from the flat list", async () => {
    const vlm: ProviderModel = {
      ...models[1],
      id: "vlm",
      name: "private-vlm",
      capability: "VLM",
    };
    const { props } = setup({ models: [vlm] });
    expect(screen.getByRole("listitem", { name: vlm.name })).toBeVisible();
    fireEvent.click(
      screen.getByRole("button", {
        name: `modelProvider.deleteModelAria:${vlm.name}`,
      }),
    );
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledWith(vlm));
    expect(props.onRemove).toHaveBeenCalledTimes(1);
    expect(vlm.capability).toBe("VLM");
  });
  it("keeps a failed removal visible and blocks repeated clicks until the request settles", async () => {
    let resolveRemoval!: (value: boolean) => void;
    const pending = new Promise<boolean>((resolve) => { resolveRemoval = resolve; });
    const onRemove = vi.fn().mockReturnValueOnce(pending).mockResolvedValueOnce(true);
    setup({ models: [models[1]], onRemove });
    const remove = screen.getByRole("button", { name: `modelProvider.deleteModelAria:${models[1].name}` });
    act(() => { fireEvent.click(remove); fireEvent.click(remove); });
    expect(onRemove).toHaveBeenCalledTimes(1);
    expect(remove).toBeDisabled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await act(async () => resolveRemoval(false));
    expect(screen.getByRole("listitem", { name: models[1].name })).toBeVisible();
    await waitFor(() => expect(remove).toBeEnabled());
    fireEvent.click(remove);
    await waitFor(() => expect(onRemove).toHaveBeenCalledTimes(2));
  });
  it("edits existing chat models with their original capability and keeps unknown logos neutral", () => {
    const unknown: ProviderModel = {
      ...models[1],
      id: "unknown",
      name: "private-lab-model",
    };
    const { props } = setup({ models: [unknown] });
    const row = screen.getByRole("listitem", { name: unknown.name });
    expect(row.querySelector("img")).toHaveAttribute(
      "src",
      "/provider-icons/custom.svg",
    );
    fireEvent.click(
      within(row).getByRole("button", {
        name: `modelProvider.editModelAria:${unknown.name}`,
      }),
    );
    expect(props.onEdit).toHaveBeenCalledWith(unknown);
    expect(props.onEdit).toHaveBeenCalledTimes(1);
    expect(props.onRemove).not.toHaveBeenCalled();
  });
  it("marks a saved OpenRouter type without an adapter as not supported while allowing reclassification", () => {
    const unsupported: ProviderModel = { ...models[1], id: "rerank", name: "vendor/rerank", capability: "RERANK" };
    const { props } = setup({ models: [unsupported], providerName: "OpenRouter" });
    expect(screen.getByText("modelProvider.catalog.notSupportedYet")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: `modelProvider.editModelAria:${unsupported.name}` }));
    expect(props.onEdit).toHaveBeenCalledWith(unsupported);
  });
});
