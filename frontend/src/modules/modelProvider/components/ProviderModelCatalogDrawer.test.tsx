import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ProviderModelCatalogDrawer } from "./ProviderModelCatalogDrawer";
import type { ProviderModel } from "../pages/ModelProvidersPage";

const mocks = vi.hoisted(() => ({ remote: vi.fn() }));
vi.mock("../api", () => ({ listRemoteGroupModels: mocks.remote }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, options?: { name?: string }) => options?.name ? `${key}:${options.name}` : key }) }));
vi.mock("@/components/request", () => ({ getLocalizedErrorMessage: (error: unknown) => error instanceof Error ? error.message : String(error) }));
vi.mock("../upstreamCatalog", () => ({ lookupUpstreamModel: () => undefined }));

const selected: ProviderModel[] = [{ id: "private", name: "qwen-private", capability: "LLM_CHAT", builtIn: false, enabled: true }];
const remote = [
  { id: "deepseek-v3", name: "deepseek-v3", model_type: "llm", max_input_tokens: "128K", added: false },
  { id: "BAAI/bge-large", name: "BAAI/bge-large", model_type: "embed", added: false },
  { id: "custom/unknown", name: "custom/unknown", model_type: "", added: false },
];

function fixture(models = selected, requiresVerification = false) {
  return {
    open: true, providerId: "supplier", providerName: "Gateway", protocolName: "OpenAI", groupId: "private-store", models,
    onClose: vi.fn(), onRefreshModels: vi.fn(), onAdd: vi.fn().mockResolvedValue(undefined), onRemove: vi.fn().mockResolvedValue(true), onChanged: vi.fn(),
    requiresVerification, onCheckConnection: vi.fn(),
  };
}
function draw(props: ReturnType<typeof fixture>) {
  return render(<ConfigProvider theme={{ token: { motion: false } }}><ProviderModelCatalogDrawer {...props} /></ConfigProvider>);
}
beforeEach(() => {
  vi.clearAllMocks();
  mocks.remote.mockResolvedValue({ models: remote });
});

describe("provider model catalog drawer", () => {
  it("asks for an explicit connection check before discovery when the saved key is unverified", async () => {
    const props = fixture(selected, true);
    draw(props);
    expect(await screen.findByText("modelProvider.catalog.verifyBeforeSync")).toBeInTheDocument();
    expect(mocks.remote).not.toHaveBeenCalled();
    expect(screen.getByText("qwen-private")).toBeInTheDocument();
    expect(screen.queryByText("modelProvider.catalog.missingRemote")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.workspace.checkConnection" }));
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
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:deepseek-v3" }));
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "deepseek-v3", model_type: "llm", max_input_tokens: "128K" })));
    expect(props.onAdd).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(props.onChanged).toHaveBeenCalledTimes(1));
  });

  it("searches vendor and model names and filters embeddings", async () => {
    draw(fixture());
    await screen.findByText("deepseek-v3");
    fireEvent.click(screen.getByRole("tab", { name: /modelProvider.catalog.embedding/ }));
    expect(screen.getByText("BAAI/bge-large")).toBeInTheDocument();
    expect(screen.queryByText("deepseek-v3")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: /modelProvider.catalog.all/ }));
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.catalog.search" }), { target: { value: "deepseek" } });
    expect(screen.getByText("deepseek-v3")).toBeInTheDocument();
    expect(screen.queryByText("BAAI/bge-large")).not.toBeInTheDocument();
  });

  it("asks before removing an embedding model and preserves it when canceled", async () => {
    const embedding: ProviderModel = { id: "embedding", name: "BAAI/bge-large", capability: "EMBEDDING", builtIn: false, enabled: true };
    const props = fixture([embedding]);
    draw(props);
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.catalog.removeModel:BAAI/bge-large" }));
    expect(await screen.findByText("modelProvider.confirmDeleteEmbeddingDesc")).toBeInTheDocument();
    expect(props.onRemove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    await waitFor(() => expect(screen.queryByText("modelProvider.confirmDeleteEmbeddingDesc")).not.toBeInTheDocument());
    expect(props.onRemove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.removeModel:BAAI/bge-large" }));
    fireEvent.click(await screen.findByRole("button", { name: "common.delete" }));
    await waitFor(() => expect(props.onRemove).toHaveBeenCalledWith(embedding));
  });

  it("does not mark saved models unlisted when discovery fails", async () => {
    mocks.remote.mockRejectedValue(new Error("discovery offline"));
    draw(fixture());
    expect(await screen.findByText("discovery offline")).toBeInTheDocument();
    expect(screen.queryByText("modelProvider.catalog.missingRemote")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "modelProvider.catalog.clearUnavailable" })).toBeDisabled();
  });

  it("adds all missing models but leaves already configured models alone", async () => {
    const props = fixture([{ id: "deepseek", name: "deepseek-v3", capability: "LLM_CHAT", builtIn: true, enabled: true }]);
    draw(props);
    await screen.findByText("BAAI/bge-large");
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.addAll" }));
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledTimes(2));
    expect(props.onAdd.mock.calls.map(([model]) => model.name)).toEqual(["BAAI/bge-large", "custom/unknown"]);
    expect(props.onRemove).not.toHaveBeenCalled();
  });

  it("lets the user specify unknown model types before adding", async () => {
    const props = fixture();
    draw(props);
    const type = await screen.findByRole("combobox", { name: "modelProvider.catalog.modelType:custom/unknown" });
    fireEvent.mouseDown(type);
    await screen.findByRole("listbox");
    fireEvent.click(screen.getByText("modelProvider.capability.embedding", { selector: ".ant-select-item-option-content" }));
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.catalog.addModel:custom/unknown" }));
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "custom/unknown", model_type: "embed" })));
  });

  it("ignores a stale discovery response after switching suppliers", async () => {
    let resolveFirst: (value: unknown) => void = () => undefined;
    mocks.remote.mockReturnValueOnce(new Promise((resolve) => { resolveFirst = resolve; })).mockResolvedValueOnce({ models: [remote[1]] });
    const props = fixture();
    const view = draw(props);
    view.rerender(<ConfigProvider theme={{ token: { motion: false } }}><ProviderModelCatalogDrawer {...props} providerId="next-supplier" /></ConfigProvider>);
    await screen.findByText("BAAI/bge-large");
    await act(async () => resolveFirst({ models: [remote[0]] }));
    expect(screen.queryByText("deepseek-v3")).not.toBeInTheDocument();
  });

  it("keeps the drawer open until an in-flight model change completes", async () => {
    let finish: () => void = () => undefined;
    const props = fixture();
    props.onAdd.mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
    const view = draw(props);
    fireEvent.click(await screen.findByRole("button", { name: "modelProvider.catalog.addModel:deepseek-v3" }));
    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();
    const mask = view.baseElement.querySelector(".ant-drawer-mask");
    if (mask) fireEvent.click(mask);
    expect(props.onClose).not.toHaveBeenCalled();
    await act(async () => finish());
    fireEvent.click(await screen.findByRole("button", { name: "Close" }));
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });
});
