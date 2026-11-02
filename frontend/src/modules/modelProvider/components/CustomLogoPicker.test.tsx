import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CustomLogoError, prepareCustomLogo } from "../customLogo";
import { CustomLogoPicker, type CustomLogoPickerProps } from "./CustomLogoPicker";

vi.mock("../customLogo", async (original) => ({ ...await original<typeof import("../customLogo")>(), prepareCustomLogo: vi.fn() }));

const png = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==";
const nextPng = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aRZkAAAAASUVORK5CYII=";
const copy = {
  label: "Logo", upload: "Upload logo", replace: "Replace logo", reset: "Restore default", hint: "PNG, JPEG or WebP, up to 2 MiB",
  errors: { empty: "Choose a nonempty image", type: "Unsupported image format", size: "Image exceeds 2 MiB", decode: "Unable to decode image", output: "Converted PNG exceeds 96 KiB" },
};
const prepare = vi.mocked(prepareCustomLogo);
function props(overrides: Partial<CustomLogoPickerProps> = {}): CustomLogoPickerProps {
  return { copy, onChange: vi.fn(), ...overrides };
}
function view(options = props()) {
  const content = (next: CustomLogoPickerProps) => <ConfigProvider theme={{ token: { motion: false } }}><CustomLogoPicker {...next} /></ConfigProvider>;
  const rendered = render(content(options));
  return { ...rendered, options, rerenderPicker: (next: CustomLogoPickerProps) => rendered.rerender(content(next)) };
}
function upload(container: HTMLElement, file = new File(["fixture"], "logo.png", { type: "image/png" })) {
  fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files: [file] } });
}
function deferred() {
  let resolve!: (value: string) => void;
  let reject!: (value: unknown) => void;
  const promise = new Promise<string>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
beforeEach(() => { vi.clearAllMocks(); prepare.mockResolvedValue(nextPng); });

describe("custom logo picker", () => {
  it("shows the default preview and exposes labeled keyboard controls", () => {
    const { container } = view();
    const group = screen.getByRole("group", { name: "Logo" });
    expect(group.querySelector("img")).toHaveAttribute("src", "/provider-icons/custom.svg");
    expect(within(group).getByRole("button", { name: "Upload logo" })).toBeEnabled();
    expect(within(group).getByRole("button", { name: "Restore default" })).toBeDisabled();
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    expect(input.accept).toBe("image/png,image/jpeg,image/webp");
    const click = vi.spyOn(input, "click").mockImplementation(() => undefined);
    fireEvent.click(within(group).getByRole("button", { name: "Logo: Upload logo" }));
    expect(click).toHaveBeenCalledOnce();
  });

  it("uses only controlled values and resets through an empty string", () => {
    const { container, options, rerenderPicker } = view(props({ value: png }));
    expect(container.querySelector("img")).toHaveAttribute("src", png);
    fireEvent.click(screen.getByRole("button", { name: "Restore default" }));
    expect(options.onChange).toHaveBeenCalledWith("");
    expect(container.querySelector("img")).toHaveAttribute("src", png);
    rerenderPicker({ ...options, value: "" });
    expect(container.querySelector("img")).toHaveAttribute("src", "/provider-icons/custom.svg");
  });

  it.each(["https://external.test/logo.png", "data:image/svg+xml,<svg/>", "data:image/png;base64,not-valid!"])("never loads an external or invalid controlled value: %s", (value) => {
    const { container } = view(props({ value }));
    expect(container.querySelector("img")).toHaveAttribute("src", "/provider-icons/custom.svg");
  });

  it("emits exactly the prepared PNG and waits for the parent to update the preview", async () => {
    const options = props({ value: png });
    const { container, rerenderPicker } = view(options);
    const file = new File(["fixture"], "logo.webp", { type: "image/webp" });
    upload(container, file);
    await waitFor(() => expect(options.onChange).toHaveBeenCalledWith(nextPng));
    expect(prepare).toHaveBeenCalledWith(file);
    expect(options.onChange).toHaveBeenCalledOnce();
    expect(container.querySelector("img")).toHaveAttribute("src", png);
    rerenderPicker({ ...options, value: nextPng });
    expect(container.querySelector("img")).toHaveAttribute("src", nextPng);
  });

  it("locks every trigger and reset while processing and permits retry after failure", async () => {
    const pending = deferred();
    prepare.mockReturnValueOnce(pending.promise);
    const options = props({ value: png });
    const { container } = view(options);
    upload(container);
    upload(container);
    expect(prepare).toHaveBeenCalledOnce();
    expect(screen.getByRole("group", { name: "Logo" })).toHaveAttribute("aria-busy", "true");
    for (const button of screen.getAllByRole("button")) expect(button).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Restore default" }));
    expect(options.onChange).not.toHaveBeenCalled();
    await act(async () => pending.reject(new CustomLogoError("decode")));
    expect(await screen.findByRole("alert")).toHaveTextContent(copy.errors.decode);
    expect(screen.getByRole("button", { name: "Replace logo" })).toBeEnabled();
    upload(container);
    await waitFor(() => expect(options.onChange).toHaveBeenCalledWith(nextPng));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it.each(["empty", "type", "size", "decode", "output"] as const)("retains the old logo and shows the injected %s error", async (code) => {
    prepare.mockRejectedValueOnce(new CustomLogoError(code));
    const { container, options } = view(props({ value: png }));
    upload(container);
    expect(await screen.findByRole("alert")).toHaveTextContent(copy.errors[code]);
    expect(container.querySelector("img")).toHaveAttribute("src", png);
    expect(options.onChange).not.toHaveBeenCalled();
  });

  it("maps unexpected decoder failures to safe copy without exposing exception text", async () => {
    prepare.mockRejectedValueOnce(new Error("private file path"));
    const { container } = view();
    upload(container);
    expect(await screen.findByRole("alert")).toHaveTextContent(copy.errors.decode);
    expect(screen.queryByText("private file path")).not.toBeInTheDocument();
  });

  it("ignores a prior upload when the controlled value changes, including changing back", async () => {
    const pending = deferred();
    prepare.mockReturnValueOnce(pending.promise);
    const options = props({ value: png });
    const { container, rerenderPicker } = view(options);
    upload(container);
    rerenderPicker({ ...options, value: nextPng });
    rerenderPicker(options);
    await act(async () => pending.resolve(nextPng));
    expect(options.onChange).not.toHaveBeenCalled();
    expect(container.querySelector("img")).toHaveAttribute("src", png);
    expect(screen.getByRole("button", { name: "Replace logo" })).toBeEnabled();
  });

  it("ignores completion after unmount", async () => {
    const pending = deferred();
    prepare.mockReturnValueOnce(pending.promise);
    const { container, options, unmount } = view();
    upload(container);
    unmount();
    await act(async () => pending.resolve(nextPng));
    expect(options.onChange).not.toHaveBeenCalled();
  });

  it("pairs busy notifications with the original observer even if the parent replaces it", async () => {
    const pending = deferred();
    prepare.mockReturnValueOnce(pending.promise);
    const original = vi.fn();
    const next = vi.fn();
    const options = props({ onBusyChange: original });
    const { container, rerenderPicker } = view(options);
    upload(container);
    expect(original).toHaveBeenCalledWith(true);
    rerenderPicker({ ...options, onBusyChange: next, value: nextPng });
    await act(async () => pending.resolve(nextPng));
    expect(original.mock.calls).toEqual([[true], [false]]);
    expect(next).not.toHaveBeenCalled();
  });

  it("releases busy once on unmount without a late completion callback", async () => {
    const pending = deferred();
    prepare.mockReturnValueOnce(pending.promise);
    const busy = vi.fn();
    const { container, unmount } = view(props({ onBusyChange: busy }));
    upload(container);
    expect(busy).toHaveBeenCalledWith(true);
    unmount();
    expect(busy.mock.calls).toEqual([[true], [false]]);
    await act(async () => pending.resolve(nextPng));
    expect(busy.mock.calls).toEqual([[true], [false]]);
  });

  it("falls back on a damaged controlled preview without changing its stored value", () => {
    const { container, options } = view(props({ value: png }));
    fireEvent.error(container.querySelector("img")!);
    expect(container.querySelector("img")).toHaveAttribute("src", "/provider-icons/custom.svg");
    expect(options.onChange).not.toHaveBeenCalled();
  });

  it("respects the parent disabled state", () => {
    const { container, options } = view(props({ value: png, disabled: true }));
    for (const button of screen.getAllByRole("button")) expect(button).toBeDisabled();
    upload(container);
    expect(prepare).not.toHaveBeenCalled();
    expect(options.onChange).not.toHaveBeenCalled();
  });
});
