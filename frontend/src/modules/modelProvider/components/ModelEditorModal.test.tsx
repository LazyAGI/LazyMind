import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { SettingsNavigationGuard } from "@/modules/settings/SettingsNavigationGuard";
import type { ModelCapability, ProviderModel } from "../pages/ModelProvidersPage";
import { ModelEditorModal } from "./ModelEditorModal";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: () => "Localized save failure",
}));

const model: ProviderModel = {
  id: "model-a",
  name: "provider/model-a",
  capability: "LLM_CHAT",
  builtIn: false,
  enabled: true,
  vision: true,
  maxInputTokens: "256K",
};
const capabilities: ModelCapability[] = ["LLM_CHAT", "VLM", "EMBEDDING", "RERANK", "ASR"];
function props(current: ProviderModel = model) {
  return {
    model: current,
    providerName: "Gateway",
    capabilities,
    models: [current],
    onSave: vi.fn().mockResolvedValue(undefined),
    onClose: vi.fn(),
    capabilityLabel: (capability: ModelCapability) => capability,
  };
}
function wrap(children: ReactNode, guard = false) {
  return <ConfigProvider theme={{ token: { motion: false } }}>
    {guard ? <MemoryRouter><SettingsNavigationGuard>{children}</SettingsNavigationGuard></MemoryRouter> : children}
  </ConfigProvider>;
}
function setup(options = props(), guard = false) {
  return { ...render(wrap(<ModelEditorModal {...options} />, guard)), options };
}
function nameInput() {
  return screen.getByRole("textbox", { name: "modelProvider.editor.apiModelId" });
}
async function visibleText(text: string) {
  const element = await screen.findByText(text);
  await waitFor(() => expect(element).toBeVisible());
  return element;
}
function saveButton() {
  return screen.getByRole("button", { name: "modelProvider.editor.save" });
}
async function chooseCapability(capability: string) {
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "modelProvider.modelType" }));
  await screen.findByRole("listbox");
  fireEvent.click(screen.getByText(capability, { selector: ".model-editor-type-option" }));
}

describe("model editor", () => {
  it("retains an unclassified model and its exact discovered context when saved", async () => {
    const options = props({ ...model, capability: "UNKNOWN", maxInputTokens: "262144" });
    setup(options);
    expect(screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).toHaveValue("262144");
    fireEvent.change(nameInput(), { target: { value: "unknown-renamed" } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith({ capability: "UNKNOWN", name: "unknown-renamed", maxInputTokens: "262144" }));
  });

  it("preserves exact discovered context when an unclassified model becomes dialogue", async () => {
    const options = props({ ...model, capability: "UNKNOWN", maxInputTokens: "262144" });
    setup(options);
    await chooseCapability("LLM_CHAT");
    expect(screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).toHaveValue("262144");
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith(expect.objectContaining({ capability: "LLM_CHAT", maxInputTokens: "262144" })));
  });

  it("leaves an unclassified model's missing context blank and permits explicit edits", async () => {
    const options = props({ ...model, capability: "UNKNOWN", maxInputTokens: undefined });
    setup(options);
    const context = screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" });
    expect(context).toHaveValue("");
    fireEvent.change(context, { target: { value: "262144" } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith({ capability: "UNKNOWN", name: model.name, maxInputTokens: "262144" }));
  });

  it("does not invent context while renaming an unclassified model without a discovered limit", async () => {
    const options = props({ ...model, capability: "UNKNOWN", maxInputTokens: undefined });
    setup(options);
    expect(screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).toHaveValue("");
    fireEvent.change(nameInput(), { target: { value: "private-renamed" } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith({ capability: "UNKNOWN", name: "private-renamed", maxInputTokens: "" }));
  });
  it("requires an OpenRouter type with an implemented adapter", async () => {
    const options = { ...props({ ...model, capability: "RERANK" }), protocolName: "OpenRouter" };
    setup(options);
    fireEvent.change(nameInput(), { target: { value: "reranker-renamed" } });
    fireEvent.click(saveButton());
    await visibleText("modelProvider.catalog.unsupportedType");
    expect(options.onSave).not.toHaveBeenCalled();
  });
  it("uses separate context, input and output presets and preserves their values", async () => {
    const options = props();
    setup(options);
    fireEvent.click(within(screen.getByRole("group", { name: "modelProvider.maxInputTokensLabel" })).getByRole("button", { name: "512K" }));
    fireEvent.click(within(screen.getByRole("group", { name: "modelProvider.editor.inputTokenLimit" })).getByRole("button", { name: "200K" }));
    fireEvent.click(within(screen.getByRole("group", { name: "modelProvider.editor.maxOutputTokens" })).getByRole("button", { name: "32K" }));
    expect(screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).toHaveValue("512K");
    expect(screen.getByRole("textbox", { name: "modelProvider.editor.inputTokenLimit" })).toHaveValue("200K");
    expect(screen.getByRole("textbox", { name: "modelProvider.editor.maxOutputTokens" })).toHaveValue("32K");
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith(expect.objectContaining({ maxInputTokens: "512K", inputTokenLimit: "200K", maxOutputTokens: "32K" })));
  });

  it("rejects directional limits larger than the context window", async () => {
    const options = props();
    setup(options);
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.editor.inputTokenLimit" }), { target: { value: "1M" } });
    fireEvent.click(saveButton());
    await visibleText("modelProvider.editor.tokenLimitExceedsContext");
    expect(options.onSave).not.toHaveBeenCalled();
  });
  it("edits a non-dialogue model without sending hidden chat parameters", async () => {
    const options = props({ ...model, capability: "RERANK" });
    setup(options);
    expect(nameInput()).toHaveValue(model.name);
    expect(screen.queryByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).not.toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    fireEvent.change(nameInput(), { target: { value: "  org/new-reranker  " } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith({ name: "org/new-reranker", capability: "RERANK" }));
    expect(options.onClose).toHaveBeenCalledTimes(1);
  });

  it("edits VLM context and preserves an existing type outside the supplied options", async () => {
    const options = { ...props({ ...model, capability: "VLM" }), capabilities: ["LLM_CHAT"] as ModelCapability[] };
    setup(options);
    const context = screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" });
    expect(context).toHaveValue("256K");
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    fireEvent.change(context, { target: { value: "1m" } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith({ name: model.name, capability: "VLM", maxInputTokens: "1M" }));
  });

  it("changes the LLM vision flag while retaining its explicit context", async () => {
    const options = props();
    setup(options);
    const vision = screen.getByRole("switch", { name: "modelProvider.vision" });
    expect(vision).toBeChecked();
    fireEvent.click(vision);
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith({
      name: model.name, capability: "LLM_CHAT", vision: false, maxInputTokens: "256K",
    }));
  });

  it("identifies a built-in customization and disables unchanged saves", async () => {
    setup(props({ ...model, builtIn: true }));
    await visibleText("modelProvider.editor.customizeHint");
    expect(saveButton()).toBeDisabled();
    fireEvent.change(nameInput(), { target: { value: "new-model-id" } });
    expect(saveButton()).toBeEnabled();
    fireEvent.change(nameInput(), { target: { value: model.name } });
    expect(saveButton()).toBeDisabled();
  });

  it("rejects a duplicate ID without confusing it with the current model", async () => {
    const options = { ...props(), models: [model, { ...model, id: "other", name: "Other-Model" }] };
    setup(options);
    fireEvent.change(nameInput(), { target: { value: " OTHER-model " } });
    fireEvent.click(saveButton());
    await visibleText("modelProvider.validation.duplicateModelName");
    expect(options.onSave).not.toHaveBeenCalled();
    fireEvent.change(nameInput(), { target: { value: model.name.toUpperCase() } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith(expect.objectContaining({ name: model.name.toUpperCase() })));
  });

  it("enforces the UTF-8 byte limit and accepts an exact 512-byte ID", async () => {
    const options = props({ ...model, capability: "EMBEDDING" });
    setup(options);
    fireEvent.change(nameInput(), { target: { value: `${"深".repeat(170)}abc` } });
    fireEvent.click(saveButton());
    await visibleText("modelProvider.editor.nameBytes");
    expect(options.onSave).not.toHaveBeenCalled();
    const valid = `${"深".repeat(170)}ab`;
    fireEvent.change(nameInput(), { target: { value: valid } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith({ name: valid, capability: "EMBEDDING" }));
  });

  it("requires an explicit valid context instead of silently saving a default", async () => {
    const options = props({ ...model, maxInputTokens: undefined });
    setup(options);
    const context = screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" });
    expect(context).toHaveValue("128K");
    fireEvent.change(context, { target: { value: "" } });
    fireEvent.click(saveButton());
    await visibleText("modelProvider.validation.maxInputTokensRequired");
    expect(options.onSave).not.toHaveBeenCalled();
    fireEvent.change(context, { target: { value: "1.5M" } });
    fireEvent.click(saveButton());
    await visibleText("modelProvider.validation.maxInputTokensInvalid");
    expect(options.onSave).not.toHaveBeenCalled();
  });

  it("drops dialogue-only fields when changing to an embedding model", async () => {
    const options = props();
    setup(options);
    await chooseCapability("EMBEDDING");
    expect(screen.queryByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).not.toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledWith({ name: model.name, capability: "EMBEDDING" }));
  });

  it("submits once and blocks dismissal while a save is pending", async () => {
    let finish: () => void = () => undefined;
    const options = props();
    options.onSave.mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
    setup(options);
    fireEvent.change(nameInput(), { target: { value: "updated-id" } });
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    fireEvent.submit(screen.getByRole("dialog").querySelector("form")!);
    await waitFor(() => expect(options.onSave).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("button", { name: "common.cancel" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();
    expect(nameInput()).toBeDisabled();
    fireEvent.keyDown(screen.getByRole("dialog").closest(".ant-modal-wrap")!, { key: "Escape", keyCode: 27 });
    expect(options.onClose).not.toHaveBeenCalled();
    await act(async () => finish());
    await waitFor(() => expect(options.onClose).toHaveBeenCalledTimes(1));
  });

  it("retains the draft and presents a localized failure before retrying", async () => {
    const options = props();
    options.onSave.mockRejectedValueOnce({ response: { data: { message: "private server detail" } } });
    setup(options);
    fireEvent.change(nameInput(), { target: { value: "draft-model" } });
    fireEvent.click(saveButton());
    await visibleText("Localized save failure");
    expect(screen.queryByText("private server detail")).not.toBeInTheDocument();
    expect(nameInput()).toHaveValue("draft-model");
    expect(options.onClose).not.toHaveBeenCalled();
    expect(saveButton()).toBeEnabled();
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledTimes(2));
    expect(options.onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps dirty edits until the user explicitly discards them", async () => {
    const options = props();
    setup(options, true);
    fireEvent.change(nameInput(), { target: { value: "unsaved-model" } });
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    await visibleText("settingsPage.unsaved.title");
    expect(options.onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "settingsPage.unsaved.stay" }));
    expect(nameInput()).toHaveValue("unsaved-model");
    fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    fireEvent.click(await screen.findByRole("button", { name: "settingsPage.unsaved.discard" }));
    expect(options.onClose).toHaveBeenCalledTimes(1);
    expect(options.onSave).not.toHaveBeenCalled();
  });

  it("keeps the draft when a background refresh returns the same model", () => {
    const options = props();
    const view = setup(options);
    fireEvent.change(nameInput(), { target: { value: "unsaved-model" } });
    view.rerender(wrap(<ModelEditorModal {...options} model={{ ...model, maxInputTokens: "512K" }} />));
    expect(nameInput()).toHaveValue("unsaved-model");
    expect(screen.getByRole("textbox", { name: "modelProvider.maxInputTokensLabel" })).toHaveValue("256K");
    expect(saveButton()).toBeEnabled();
  });

  it("ignores completion of an old model save after the editor context changes", async () => {
    let finish: () => void = () => undefined;
    const options = props();
    options.onSave.mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
    const view = setup(options);
    fireEvent.change(nameInput(), { target: { value: "draft-first" } });
    fireEvent.click(saveButton());
    await waitFor(() => expect(options.onSave).toHaveBeenCalledTimes(1));
    const next = { ...model, id: "model-b", name: "second-model" };
    view.rerender(wrap(<ModelEditorModal {...options} model={next} models={[next]} />));
    expect(nameInput()).toHaveValue("second-model");
    await act(async () => finish());
    expect(options.onClose).not.toHaveBeenCalled();
    expect(nameInput()).toHaveValue("second-model");
  });
});
