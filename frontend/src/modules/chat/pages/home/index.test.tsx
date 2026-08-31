import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import Home from "./index";

vi.mock("../newChat", () => ({
  default: () => <div>New chat</div>,
}));

describe("Home", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    Object.defineProperty(window, "lazymindDesktop", {
      configurable: true,
      value: undefined,
    });
  });

  it("notifies the desktop as soon as the Chat home mounts", () => {
    const notifyAppReady = vi.fn();
    const requestAnimationFrame = vi.spyOn(window, "requestAnimationFrame");
    Object.defineProperty(window, "lazymindDesktop", {
      configurable: true,
      value: { notifyAppReady },
    });

    render(<Home />);

    expect(screen.getByText("New chat")).toBeInTheDocument();
    expect(notifyAppReady).toHaveBeenCalledOnce();
    expect(requestAnimationFrame).not.toHaveBeenCalled();
  });

  it("still renders outside the desktop shell", () => {
    expect(() => render(<Home />)).not.toThrow();
    expect(screen.getByText("New chat")).toBeInTheDocument();
  });
});
