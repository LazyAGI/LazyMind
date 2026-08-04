import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import ShowChatFileList from "./index";
import type { ChatFileList } from "../ChatInput";

function makeFile(overrides: Partial<ChatFileList>): ChatFileList {
  return {
    uid: "uid-1",
    name: "file.png",
    base64: "data:image/png;base64,aaa",
    suffix: ".png",
    size: "1.00 KB",
    ...overrides,
  };
}

describe("ShowChatFileList", () => {
  it("renders every file as an image item when the whole list is images", () => {
    render(
      <ShowChatFileList
        fileList={[
          makeFile({ uid: "u1", name: "a.png", suffix: ".png" }),
          makeFile({ uid: "u2", name: "b.jpg", suffix: ".jpg" }),
        ]}
        onRemove={vi.fn()}
      />,
    );
    expect(document.querySelectorAll(".chat-images-item")).toHaveLength(2);
  });

  it("renders non-image files using the file-row layout with name and size", () => {
    render(
      <ShowChatFileList
        fileList={[
          makeFile({
            uid: "u1",
            name: "report.pdf",
            base64: "",
            previewUrl: "blob:report",
            suffix: ".pdf",
            size: "2.00 MB",
          }),
        ]}
        onRemove={vi.fn()}
      />,
    );
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.getByText("2.00 MB")).toBeInTheDocument();
  });

  it("opens a non-image file preview in a new tab when clicked", () => {
    const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);
    render(
      <ShowChatFileList
        fileList={[
          makeFile({
            uid: "u1",
            name: "report.pdf",
            base64: "",
            previewUrl: "blob:report",
            suffix: ".pdf",
          }),
        ]}
        onRemove={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button"));

    expect(openSpy).toHaveBeenCalledWith("blob:report", "_blank", "noopener,noreferrer");
    openSpy.mockRestore();
  });

  it("calls onRemove with the item uid when the remove icon is clicked", () => {
    const onRemove = vi.fn();
    render(
      <ShowChatFileList
        fileList={[makeFile({ uid: "u1", name: "a.png", suffix: ".png" })]}
        onRemove={onRemove}
      />,
    );

    fireEvent.click(document.querySelector(".chat-files-remove") as Element);
    expect(onRemove).toHaveBeenCalledWith("u1");
  });

  it("renders mixed lists with both file and image rows when a non-image file is present", () => {
    render(
      <ShowChatFileList
        fileList={[
          makeFile({ uid: "u1", name: "a.png", suffix: ".png" }),
          makeFile({
            uid: "u2",
            name: "doc.pdf",
            base64: "",
            previewUrl: "blob:doc",
            suffix: ".pdf",
          }),
        ]}
        onRemove={vi.fn()}
      />,
    );
    expect(screen.getByText("doc.pdf")).toBeInTheDocument();
    expect(document.querySelectorAll(".chat-files-item")).toHaveLength(2);
  });
});
