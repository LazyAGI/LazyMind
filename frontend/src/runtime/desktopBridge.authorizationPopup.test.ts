import { afterEach, describe, expect, it, vi } from "vitest";

import {
  openCloudLogin,
  reserveCloudLoginPopup,
} from "./desktopBridge";

describe("Compose Web Cloud authorization popup", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("reserves a blank popup synchronously and navigates it only after URL validation", async () => {
    const popup = {
      closed: false,
      close: vi.fn(),
      opener: window,
      location: { replace: vi.fn() },
    };
    const open = vi
      .spyOn(window, "open")
      .mockReturnValue(popup as unknown as Window);

    const reserved = reserveCloudLoginPopup();

    expect(open).toHaveBeenCalledWith(
      "about:blank",
      "_blank",
    );
    expect(popup.opener).toBeNull();
    const authorizationURL =
      "https://localhost:8443/zh/desktop/authorize?client_id=lazymind-desktop";
    await expect(openCloudLogin(authorizationURL, reserved)).resolves.toEqual({
      ok: true,
    });
    expect(popup.location.replace).toHaveBeenCalledWith(authorizationURL);
    expect(open).toHaveBeenCalledTimes(1);
  });
});
