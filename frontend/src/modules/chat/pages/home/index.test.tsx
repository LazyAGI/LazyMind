import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import Home from "./index";

vi.mock("../newChat", () => ({
  default: () => <div data-testid="new-chat-page-stub" />,
}));

describe("Home", () => {
  it("renders the new chat page inside the chat wrapper", () => {
    renderWithProviders(<Home />);
    expect(screen.getByTestId("new-chat-page-stub")).toBeInTheDocument();
    expect(document.querySelector(".chat-wrapper")).toBeInTheDocument();
  });

  it("notifies the desktop shell that the app is ready via requestAnimationFrame", () => {
    const notifyAppReady = vi.fn();
    (window as any).lazymindDesktop = { notifyAppReady };
    const rafSpy = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation((cb: FrameRequestCallback) => {
        cb(0);
        return 1;
      });

    renderWithProviders(<Home />);

    expect(notifyAppReady).toHaveBeenCalled();
    rafSpy.mockRestore();
    delete (window as any).lazymindDesktop;
  });
});
