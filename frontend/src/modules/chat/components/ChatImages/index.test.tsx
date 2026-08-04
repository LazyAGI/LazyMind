import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import ChatImages, { type ChatImage } from "./index";

const images: ChatImage[] = [
  { base64: "data:image/png;base64,aaa", uid: "img-1" },
  { base64: "data:image/png;base64,bbb", uid: "img-2" },
];

describe("ChatImages", () => {
  it("renders an image element for every image in the list", () => {
    render(<ChatImages images={images} />);
    const items = document.querySelectorAll(".chat-images-item");
    expect(items).toHaveLength(2);
    expect(items[0].querySelector("img")).toHaveAttribute("src", images[0].base64);
  });

  it("does not render a remove control when onRemove is not provided", () => {
    render(<ChatImages images={images} />);
    expect(document.querySelector(".chat-images-remove")).not.toBeInTheDocument();
  });

  it("invokes onRemove with the image uid when the remove control is clicked", () => {
    const onRemove = vi.fn();
    render(<ChatImages images={images} onRemove={onRemove} />);

    const removeButtons = document.querySelectorAll(".chat-images-remove");
    expect(removeButtons).toHaveLength(2);
    fireEvent.click(removeButtons[1]);

    expect(onRemove).toHaveBeenCalledWith("img-2");
  });

  it("renders nothing when the images list is empty", () => {
    render(<ChatImages images={[]} />);
    expect(screen.queryAllByRole("img")).toHaveLength(0);
  });
});
