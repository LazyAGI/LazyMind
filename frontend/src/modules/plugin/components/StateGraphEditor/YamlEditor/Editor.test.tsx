import { render, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import Editor from "./Editor";
import type { ValidationError } from "../core/validator";

const setModelMarkers = vi.fn();
const onDidChangeModelContent = vi.fn();
const getValue = vi.fn(() => "content");
const dispose = vi.fn();
const getModel = vi.fn(() => ({
  getValue: () => "content",
  pushEditOperations: vi.fn(),
  getFullModelRange: vi.fn(),
}));
const getSelections = vi.fn(() => []);

const create = vi.fn(() => ({
  dispose,
  getValue,
  getModel,
  getSelections,
  onDidChangeModelContent,
}));

vi.mock("monaco-editor", () => ({
  editor: {
    create,
    setModelMarkers,
  },
  MarkerSeverity: { Error: 8 },
}));

describe("YamlEditor/Editor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("creates a monaco editor instance mounted on the container", async () => {
    const onChange = vi.fn();
    render(<Editor value="foo: bar" onChange={onChange} errors={[]} />);

    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    const [, options] = create.mock.calls[0];
    expect(options).toMatchObject({ value: "foo: bar", language: "yaml", readOnly: false });
  });

  it("forwards editor content changes via onChange", async () => {
    const onChange = vi.fn();
    render(<Editor value="foo: bar" onChange={onChange} errors={[]} />);

    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    const changeHandler = onDidChangeModelContent.mock.calls[0][0];
    changeHandler();

    expect(onChange).toHaveBeenCalledWith("content");
  });

  it("pushes validation errors as monaco markers", async () => {
    const errors: ValidationError[] = [
      { code: "bad", message: "something is wrong", line: 3 },
    ];
    const { rerender } = render(<Editor value="foo: bar" onChange={vi.fn()} errors={[]} />);
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));

    rerender(<Editor value="foo: bar" onChange={vi.fn()} errors={errors} />);

    await waitFor(() => expect(setModelMarkers).toHaveBeenCalled());
    const [, , markers] = setModelMarkers.mock.calls[setModelMarkers.mock.calls.length - 1];
    expect(markers[0]).toMatchObject({ message: "something is wrong", startLineNumber: 3 });
  });

  it("disposes the editor instance on unmount", async () => {
    const { unmount } = render(<Editor value="foo: bar" onChange={vi.fn()} errors={[]} />);
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));

    unmount();

    expect(dispose).toHaveBeenCalled();
  });
});
