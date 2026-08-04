import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import ChatFiles, { type ChatFile } from "./index";

const files: ChatFile[] = [
  { name: "a.txt", uid: "u1" },
  { name: "b.txt", uid: "u2" },
];

describe("ChatFiles", () => {
  it("renders a row with the file name for each file", () => {
    render(<ChatFiles files={files} />);
    expect(screen.getByText("a.txt")).toBeInTheDocument();
    expect(screen.getByText("b.txt")).toBeInTheDocument();
  });

  it("does not render a remove control without onRemove", () => {
    render(<ChatFiles files={files} />);
    expect(document.querySelector(".chat-files-remove")).not.toBeInTheDocument();
  });

  it("invokes onRemove with the clicked file's uid", () => {
    const onRemove = vi.fn();
    render(<ChatFiles files={files} onRemove={onRemove} />);
    const removeButtons = document.querySelectorAll(".chat-files-remove");
    fireEvent.click(removeButtons[1]);
    expect(onRemove).toHaveBeenCalledWith("u2");
  });

  it("renders nothing when the files list is empty", () => {
    render(<ChatFiles files={[]} />);
    expect(document.querySelectorAll(".chat-files-item")).toHaveLength(0);
  });
});
