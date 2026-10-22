import {
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

const mocks = vi.hoisted(() => ({ lookup: vi.fn() }));
vi.mock("../upstreamCatalog", () => ({ lookupUpstreamModel: mocks.lookup }));
beforeEach(() => {
  mocks.lookup.mockReset();
});

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, values?: { name?: string }) =>
      values?.name ? `${key}:${values.name}` : key,
  }),
}));
vi.mock("./ModelModalityMarks", () => ({
  ModelModalityMarks: () => <span data-testid="modalities" />,
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

describe("saved supplier models", () => {
  it("keeps custom models globally above built-ins even when they share a brand", () => {
    const customGpt = { ...models[1], name: "gpt-4o-custom" };
    const builtInGpt = {
      ...models[1],
      id: "builtin-gpt",
      name: "gpt-4o-builtin",
      builtIn: true,
    };
    const { container } = setup({ models: [builtInGpt, customGpt, models[2]] });
    expect(
      screen
        .getAllByRole("listitem")
        .map((row) => row.getAttribute("aria-label")),
    ).toEqual(["gpt-4o-custom", "BAAI/bge-large", "gpt-4o-builtin"]);
    expect(
      [...container.querySelectorAll(".saved-provider-model-group")].map(
        (group) => group.getAttribute("aria-label"),
      ),
    ).toEqual(["OpenAI", "BAAI", "OpenAI"]);
  });
  it("keeps explicitly configured vision support visible for models outside the catalog", () => {
    setup({
      models: [
        {
          id: "vision",
          name: "private-vision-model",
          capability: "LLM_CHAT",
          builtIn: false,
          enabled: true,
          vision: true,
        },
      ],
    });
    expect(
      screen.getByLabelText("modelProvider.visionSupported"),
    ).toBeVisible();
  });
  it("does not duplicate a vision marker when catalog modalities already include image input", () => {
    mocks.lookup.mockReturnValue({ input: ["text", "image"] });
    setup({ models: [{ ...models[1], vision: true }] });
    expect(
      screen.queryByLabelText("modelProvider.visionSupported"),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId("modalities")).toBeInTheDocument();
  });
  it("groups by model brand and gives saved rows their own logos while retaining custom-first order", () => {
    const { container } = setup();
    const groups = container.querySelectorAll(".saved-provider-model-group");
    expect(groups[0]).toHaveAttribute("aria-label", "OpenAI");
    expect(groups[1]).toHaveAttribute("aria-label", "BAAI");
    expect(groups[2]).toHaveAttribute("aria-label", "DeepSeek");
    const gpt = screen.getByRole("listitem", { name: "gpt-4o" });
    expect(gpt.querySelector("img")).toHaveAttribute(
      "src",
      "/provider-icons/openai.svg",
    );
    expect(gpt.querySelector("img")).toHaveAttribute("alt", "");
    expect(gpt).toHaveTextContent("1M");
    expect(container.querySelector("[role=table]")).toBeNull();
  });
  it("collapses only the selected brand and keeps a stable model count", () => {
    setup();
    const group = screen.getByRole("region", { name: "OpenAI" });
    const toggle = within(group).getByRole("button", { name: /OpenAI/ });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(
      screen.queryByRole("listitem", { name: "gpt-4o" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("listitem", { name: "deepseek-catalog" }),
    ).toBeInTheDocument();
    fireEvent.click(toggle);
    expect(
      screen.getByRole("listitem", { name: "gpt-4o" }),
    ).toBeInTheDocument();
  });
  it("keeps embedding removal behind its existing impact confirmation", async () => {
    const { props } = setup();
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.deleteModelAria:BAAI/bge-large",
      }),
    );
    const warning = await screen.findByText(
      "modelProvider.confirmDeleteEmbeddingDesc",
    );
    await waitFor(() => expect(warning).toBeVisible());
    expect(props.onRemove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    await waitFor(() => expect(warning).not.toBeVisible());
    expect(props.onRemove).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", {
        name: "modelProvider.deleteModelAria:BAAI/bge-large",
      }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "common.delete" }),
    );
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledWith(models[2]));
  });
  it("edits only editable chat models and keeps unknown logos neutral", () => {
    const unknown: ProviderModel = {
      id: "unknown",
      name: "private-lab-model",
      capability: "LLM_CHAT",
      builtIn: false,
      enabled: true,
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
    expect(props.onRemove).not.toHaveBeenCalled();
  });
});
