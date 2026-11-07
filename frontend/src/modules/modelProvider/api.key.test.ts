import { afterEach, describe, expect, it, vi } from "vitest";
import { modelProvidersApi, replaceProviderKey } from "./api";

afterEach(() => vi.restoreAllMocks());

describe("provider key API", () => {
  it("keeps duplicate replacement errors local to the key editor", async () => {
    const request = vi.spyOn(modelProvidersApi, "apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysKeyIdPut")
      .mockRejectedValue(new Error("api_key already exists"));

    await expect(replaceProviderKey("supplier", "group", "key", "already-saved-key"))
      .rejects.toThrow("api_key already exists");
    expect(request).toHaveBeenCalledWith(expect.objectContaining({
      modelProviderId: "supplier",
      groupId: "group",
      keyId: "key",
      replaceModelProviderKeyOpenAPIRequest: { api_key: "already-saved-key" },
    }), expect.objectContaining({ timeout: 180000, silentError: true }));
  });
});
