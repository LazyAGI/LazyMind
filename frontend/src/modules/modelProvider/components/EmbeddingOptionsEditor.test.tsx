import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Button, ConfigProvider, Form } from "antd";
import { describe, expect, it, vi } from "vitest";
import { EmbeddingOptionsEditor } from "./EmbeddingOptionsEditor";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

function setup() {
  const save = vi.fn();
  render(<ConfigProvider theme={{ token: { motion: false } }}><Form onFinish={save}>
    <EmbeddingOptionsEditor /><Button htmlType="submit">Save</Button>
  </Form></ConfigProvider>);
  return save;
}

describe("embedding options", () => {
  it("preserves prefix whitespace and submits a numeric batch size", async () => {
    const save = setup();
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.editor.embeddingQueryPrefix" }), { target: { value: "  query: " } });
    fireEvent.change(screen.getByRole("textbox", { name: "modelProvider.editor.embeddingPassagePrefix" }), { target: { value: "passage:  " } });
    fireEvent.change(screen.getByRole("spinbutton", { name: "modelProvider.editor.embeddingBatchSize" }), { target: { value: "16" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(save).toHaveBeenCalledWith({ embeddingQueryPrefix: "  query: ", embeddingPassagePrefix: "passage:  ", embeddingBatchSize: 16 }));
  });

  it("blocks a fractional batch size", async () => {
    const save = setup();
    fireEvent.change(screen.getByRole("spinbutton", { name: "modelProvider.editor.embeddingBatchSize" }), { target: { value: "1.5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("modelProvider.editor.embeddingBatchSizeInvalid")).toBeVisible();
    expect(save).not.toHaveBeenCalled();
  });

  it("accepts 2048 Unicode characters and rejects a longer prefix", async () => {
    const save = setup();
    const query = screen.getByRole("textbox", { name: "modelProvider.editor.embeddingQueryPrefix" });
    const prefix = "🦉".repeat(2048);
    fireEvent.change(query, { target: { value: prefix } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({ embeddingQueryPrefix: prefix })));
    save.mockClear();
    fireEvent.change(query, { target: { value: prefix + "x" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("modelProvider.editor.embeddingPrefixInvalid")).toBeVisible();
    expect(save).not.toHaveBeenCalled();
  });
});
