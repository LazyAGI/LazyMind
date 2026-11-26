import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { createPreviewRenderer } from "./createPreviewRenderer";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

type Props = { label: string; onPreviewError?: () => void };
const Document = ({ label }: Props) => <p>{label}</p>;

describe("preview renderer module loading", () => {
  it("keeps a failed module local, reports it, and retries without replacing the page", async () => {
    const load = vi.fn().mockRejectedValueOnce(new Error("Chunk unavailable"))
      .mockResolvedValue({ default: Document });
    const Preview = createPreviewRenderer<Props>(load);
    const onPreviewError = vi.fn();
    render(<><button>Page navigation</button><Preview label="Document content" onPreviewError={onPreviewError} /></>);
    expect(await screen.findByRole("alert")).toHaveTextContent("knowledge.previewLoadFailed");
    expect(screen.getByRole("button", { name: "Page navigation" })).toBeInTheDocument();
    expect(onPreviewError).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "common.retry" }));
    expect(await screen.findByText("Document content")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("shows failure without a caller callback and reloads on remount", async () => {
    const load = vi.fn().mockRejectedValueOnce(new Error("Chunk unavailable"))
      .mockResolvedValue({ default: Document });
    const Preview = createPreviewRenderer<Props>(load);
    const view = render(<Preview label="First document" />);
    await screen.findByRole("alert");
    view.unmount();
    render(<Preview label="New document" />);
    expect(await screen.findByText("New document")).toBeInTheDocument();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("reuses a successfully loaded renderer while passing current document props", async () => {
    const load = vi.fn().mockResolvedValue({ default: Document });
    const Preview = createPreviewRenderer<Props>(load);
    const view = render(<Preview label="First document" />);
    await screen.findByText("First document");
    view.rerender(<Preview label="Second document" />);
    expect(screen.getByText("Second document")).toBeInTheDocument();
    view.unmount();
    render(<Preview label="Third document" />);
    expect(screen.getByText("Third document")).toBeInTheDocument();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("ignores a late failure after the preview has unmounted", async () => {
    let reject!: (error: Error) => void;
    const load = vi.fn(() => new Promise<{ default: typeof Document }>((_, fail) => { reject = fail; }));
    const Preview = createPreviewRenderer<Props>(load);
    const onPreviewError = vi.fn();
    const view = render(<Preview label="Document" onPreviewError={onPreviewError} />);
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    view.unmount();
    await act(async () => { reject(new Error("Chunk unavailable")); });
    expect(onPreviewError).not.toHaveBeenCalled();
  });
});
