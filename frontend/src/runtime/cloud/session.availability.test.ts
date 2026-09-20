import { beforeEach, describe, expect, it, vi } from "vitest";

import { getCloudSession } from "./session";

const get = vi.hoisted(() => vi.fn());

vi.mock("@/components/request", () => ({
  axiosInstance: { defaults: {}, request: (options: { url: string }) => get(options.url, options) },
  BASE_URL: "/desktop",
}));

describe("Desktop Cloud session availability snapshot", () => {
  beforeEach(() => get.mockReset());

  it("queries the local snapshot silently and preserves configuration and reachability", async () => {
    get.mockResolvedValueOnce({
      data: {
        data: {
          configured: true,
          reachability: "unreachable",
          state: "signed_out",
        },
      },
    });

    const session = await getCloudSession();

    expect(get).toHaveBeenCalledWith(
      "/desktop/api/core/cloud/session",
      expect.objectContaining({ silentError: true }),
    );
    expect(session).toEqual(expect.objectContaining({
      configured: true,
      reachability: "unreachable",
      state: "signed_out",
    }));
  });
});
