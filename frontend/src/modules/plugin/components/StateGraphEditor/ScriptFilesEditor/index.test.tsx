import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ScriptFilesEditor from "./index";

const create = vi.fn(() => ({
  dispose: vi.fn(),
  getValue: vi.fn(() => ""),
  setValue: vi.fn(),
  onDidChangeModelContent: vi.fn(),
}));

vi.mock("monaco-editor", () => ({
  editor: { create },
}));

describe("ScriptFilesEditor", () => {
  it("shows the empty hint when there are no script files", () => {
    render(<ScriptFilesEditor value="{}" onChange={vi.fn()} />);
    expect(screen.getByText("selfEvolutionRun.scriptFilesEmpty")).toBeInTheDocument();
  });

  it("parses the initial script map and selects the first file", async () => {
    render(
      <ScriptFilesEditor
        value={JSON.stringify({ "scripts/tools.py": "print(1)" })}
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByText("tools.py")).toBeInTheDocument();
    await waitFor(() => expect(create).toHaveBeenCalled());
  });

  it("falls back to an empty script map for malformed JSON", () => {
    render(<ScriptFilesEditor value="not json" onChange={vi.fn()} />);
    expect(screen.getByText("selfEvolutionRun.scriptFilesEmpty")).toBeInTheDocument();
  });

  it("adds a new file, prefixing the scripts/ path, and emits the updated map", () => {
    const onChange = vi.fn();
    const { container } = render(<ScriptFilesEditor value="{}" onChange={onChange} />);

    fireEvent.click(container.querySelector(".sfe-sidebar-header button")!);
    const input = screen.getByPlaceholderText("tools.py");
    fireEvent.change(input, { target: { value: "helper.py" } });
    fireEvent.click(screen.getByRole("button", { name: "selfEvolutionRun.scriptFilesAdd" }));

    expect(onChange).toHaveBeenCalledWith(JSON.stringify({ "scripts/helper.py": "" }));
    expect(screen.getByText("helper.py")).toBeInTheDocument();
  });

  it("deletes a file and selects the next remaining one", () => {
    const onChange = vi.fn();
    render(
      <ScriptFilesEditor
        value={JSON.stringify({ "scripts/a.py": "1", "scripts/b.py": "2" })}
        onChange={onChange}
      />,
    );

    const deleteButtons = screen.getAllByRole("button").filter((btn) =>
      btn.className.includes("sfe-file-delete"),
    );
    fireEvent.click(deleteButtons[0]);

    expect(onChange).toHaveBeenCalledWith(JSON.stringify({ "scripts/b.py": "2" }));
  });
});
