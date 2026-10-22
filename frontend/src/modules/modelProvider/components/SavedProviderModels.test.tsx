import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import { describe, expect, it, vi } from "vitest";
import type { ProviderModel } from "../pages/ModelProvidersPage";
import {
  SavedProviderModels,
  type SavedProviderModelsProps,
} from "./SavedProviderModels";

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
  it("shows a saved visual dialogue context window without changing its edit permissions", async () => {
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
      screen.getByRole("button", { name: "private-vlm", exact: true }),
    );
    const context = await screen.findByText(
      "modelProvider.workspace.context: 200000",
    );
    await waitFor(() => expect(context).toBeVisible());
    expect(
      screen.queryByRole("button", {
        name: "modelProvider.editModelWindowAria:private-vlm",
      }),
    ).not.toBeInTheDocument();
  });
  it("keeps all custom task groups above built-ins and collapses repeated task groups independently", () => {
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
    expect(groupLabels(container)).toEqual([
      "modelProvider.taskType.chat",
      "modelProvider.taskType.embedding",
      "modelProvider.taskType.chat",
    ]);
    const chatGroups = screen.getAllByRole("region", {
      name: "modelProvider.taskType.chat",
    });
    const toggle = within(chatGroups[0]).getByRole("button", {
      name: /modelProvider.taskType.chat/,
    });
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(
      screen.queryByRole("listitem", { name: customChat.name }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("listitem", { name: builtInChat.name }),
    ).toBeVisible();
    fireEvent.click(toggle);
    expect(
      screen.getByRole("listitem", { name: customChat.name }),
    ).toBeVisible();
  });
  it("groups LLM, VLM, and self-evolution together while keeping embedding and rerank separate", () => {
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
    expect(groupLabels(container)).toEqual([
      "modelProvider.taskType.chat",
      "modelProvider.taskType.embedding",
      "modelProvider.taskType.rerank",
    ]);
    expect(
      within(
        screen.getByRole("region", { name: "modelProvider.taskType.chat" }),
      ).getAllByRole("listitem"),
    ).toHaveLength(3);
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
        name: `modelProvider.editModelWindowAria:${vlm.name}`,
      }),
    ).not.toBeInTheDocument();
    expect(container.querySelector("[role=table]")).toBeNull();
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
    "keeps %s removal behind its impact confirmation",
    async (capability) => {
      const embedding: ProviderModel = { ...models[2], capability };
      const { props } = setup({ models: [embedding] });
      fireEvent.click(
        screen.getByRole("button", {
          name: `modelProvider.deleteModelAria:${embedding.name}`,
        }),
      );
      const warning = await screen.findByText(
        capability === "EMBEDDING"
          ? "modelProvider.confirmDeleteEmbeddingDesc"
          : "modelProvider.confirmDeleteModelDesc",
      );
      await waitFor(() => expect(warning).toBeVisible());
      expect(props.onRemove).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
      await waitFor(() => expect(warning).not.toBeVisible());
      expect(props.onRemove).not.toHaveBeenCalled();
      fireEvent.click(
        screen.getByRole("button", {
          name: `modelProvider.deleteModelAria:${embedding.name}`,
        }),
      );
      fireEvent.click(
        await screen.findByRole("button", { name: "common.delete" }),
      );
      await waitFor(() =>
        expect(props.onRemove).toHaveBeenCalledWith(embedding),
      );
      expect(props.onRemove).toHaveBeenCalledTimes(1);
    },
  );
  it("retains VLM's underlying capability when confirming removal from the chat group", async () => {
    const vlm: ProviderModel = {
      ...models[1],
      id: "vlm",
      name: "private-vlm",
      capability: "VLM",
    };
    const { props } = setup({ models: [vlm] });
    expect(
      screen.getByRole("region", { name: "modelProvider.taskType.chat" }),
    ).toBeVisible();
    fireEvent.click(
      screen.getByRole("button", {
        name: `modelProvider.deleteModelAria:${vlm.name}`,
      }),
    );
    const description = await screen.findByText(
      "modelProvider.confirmDeleteModelDesc",
    );
    await waitFor(() => expect(description).toBeVisible());
    fireEvent.click(
      await screen.findByRole("button", { name: "common.delete" }),
    );
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledWith(vlm));
    expect(props.onRemove).toHaveBeenCalledTimes(1);
    expect(vlm.capability).toBe("VLM");
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
        name: `modelProvider.editModelWindowAria:${unknown.name}`,
      }),
    );
    expect(props.onEdit).toHaveBeenCalledWith(unknown);
    expect(props.onEdit).toHaveBeenCalledTimes(1);
    expect(props.onRemove).not.toHaveBeenCalled();
  });
});
