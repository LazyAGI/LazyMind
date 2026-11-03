import { useState } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import PaperSearch from "./PaperSearch";

function Harness() {
  const [open, setOpen] = useState(true);
  const [value, setValue] = useState("");
  return <PaperSearch open={open} value={value} onOpenChange={setOpen} onChange={setValue} />;
}

afterEach(cleanup);

describe("PaperSearch keyboard input", () => {
  it.each([
    ["Enter", { isComposing: true, keyCode: 13 }],
    ["Enter", { isComposing: false, keyCode: 229 }],
    ["Escape", { isComposing: true, keyCode: 27 }],
    ["Escape", { isComposing: false, keyCode: 229 }],
  ] as const)("keeps search open for IME %s (%j)", async (key, composition) => {
    render(<Harness />);
    const input = screen.getByRole("textbox", { name: "搜索文献" });
    fireEvent.compositionStart(input);
    fireEvent.change(input, { target: { value: "论文" } });
    fireEvent.keyDown(input, { key, ...composition });

    expect(screen.getByRole("textbox", { name: "搜索文献" })).toBe(input);
    expect(input).toHaveFocus();
    expect(input).toHaveValue("论文");

    fireEvent.compositionEnd(input, { data: "论文" });
    fireEvent.keyUp(input, { key });
    fireEvent.keyDown(input, { key });
    expect(screen.queryByRole("textbox", { name: "搜索文献" })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "搜索" })).toHaveFocus());
  });
});
