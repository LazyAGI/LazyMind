import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import LoginTransition from "./index";

vi.mock("./index.scss", () => ({}));
vi.mock("@/components/request", () => ({
  BASE_URL: "http://mock-base",
  getLocalizedErrorMessage: vi.fn(() => "localized error"),
  localizeErrorCode: vi.fn((code: string) => `error:${code}`),
}));

function renderWithSearch(search: string) {
  return render(
    <MemoryRouter initialEntries={[`/login-transition${search}`]}>
      <LoginTransition />
    </MemoryRouter>,
  );
}

describe("LoginTransition page", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn());
    vi.stubGlobal("location", { ...window.location, replace: vi.fn(), origin: "http://app.test" });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("skips the exchange call and stops loading when there is no code param", async () => {
    renderWithSearch("");
    await waitFor(() => expect(fetch).not.toHaveBeenCalled());
    expect(screen.getByText("auth.retryLogin")).toBeInTheDocument();
  });

  it("follows the redirected response's url when the fetch response was redirected", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      redirected: true,
      url: "http://app.test/agent/chat",
      json: async () => ({}),
      ok: true,
    });

    renderWithSearch("?code=abc123");

    await waitFor(() =>
      expect(window.location.replace).toHaveBeenCalledWith("http://app.test/agent/chat"),
    );
  });

  it("redirects using the redirect_to field from the JSON payload", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      redirected: false,
      url: "",
      json: async () => ({ redirect_to: "/agent/chat" }),
      ok: true,
    });

    renderWithSearch("?code=abc123");

    await waitFor(() =>
      expect(window.location.replace).toHaveBeenCalledWith("/agent/chat"),
    );
  });

  it("shows a localized error message when the response is not ok and has no redirect", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      redirected: false,
      url: "",
      json: async () => ({ code: "9999" }),
      ok: false,
      status: 400,
    });
    const { message } = await import("antd");
    const errorSpy = vi.spyOn(message, "error");

    renderWithSearch("?code=abc123");

    await waitFor(() => expect(errorSpy).toHaveBeenCalledWith("localized error"));
  });

  it("shows the generic error message when the fetch call throws", async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(new Error("network down"));
    const { message } = await import("antd");
    const errorSpy = vi.spyOn(message, "error");

    renderWithSearch("?code=abc123");

    await waitFor(() => expect(errorSpy).toHaveBeenCalledWith("error:2000509"));
  });

  it("clears the login_challenge cookie and redirects to /login when retrying", () => {
    renderWithSearch("");
    fireEvent.click(screen.getByText("auth.retryLogin"));
    expect(window.location.replace).toHaveBeenCalledWith("http://app.test/login");
  });
});
