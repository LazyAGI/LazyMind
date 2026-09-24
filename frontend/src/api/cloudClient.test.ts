import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ adapter: vi.fn() }));
vi.mock("@/components/request", async () => {
  const { default: axios } = await import("axios");
  return { BASE_URL: "http://localhost:9000", axiosInstance: axios.create({ adapter: mocks.adapter }) };
});

import { getCloudSession, beginCloudLogin, logoutCloudSession } from "@/runtime/cloud/session";
import { getCloudResource, getCloudResourceTree, getCloudResourceContent, downloadCloudResource, uploadCloudSkill } from "@/modules/memory/cloudResourceApi";
import { fetchCloudTokenPlan } from "@/modules/settings/cloudUsageApi";
import { getCredentialBackupStatus, setCredentialBackupEnabled, getCredentialRestoreDiscovery, startCredentialRestore, getCredentialRestoreOperation, cancelCredentialRestore } from "@/modules/modelProvider/api";

beforeEach(() => {
  mocks.adapter.mockReset().mockImplementation(async (config) => ({
    config, data: { data: config.url.endsWith("/cloud/token-plan") ? { status: "inactive" } : { authorization_url: "https://example.com/authorize", status: "inactive" } },
    status: 200, statusText: "OK", headers: {},
  }));
});

describe("Cloud APIs use generated request contracts", () => {
  it("propagates local logout cleanup failure without attempting login or retry", async () => {
    const failure = { response: { status: 503, data: { code: 2000000, message: "Internal server error" } } };
    mocks.adapter.mockRejectedValueOnce(failure);
    await expect(logoutCloudSession()).rejects.toBe(failure);
    expect(mocks.adapter).toHaveBeenCalledOnce();
    expect(mocks.adapter.mock.calls[0][0]).toMatchObject({ method: "post", url: "http://localhost:9000/api/core/cloud/logout" });
  });

  it.each([
    [getCloudSession, "get", "/cloud/session"], [beginCloudLogin, "post", "/cloud/login"],
    [logoutCloudSession, "post", "/cloud/logout"], [getCredentialBackupStatus, "get", "/credential-vault/backup"],
    [() => setCredentialBackupEnabled(true), "post", "/credential-vault/backup:enable"],
    [() => setCredentialBackupEnabled(false), "post", "/credential-vault/backup:disable"],
    [getCredentialRestoreDiscovery, "get", "/credential-vault/restores"],
  ] as const)("routes a Cloud operation through Core (%s)", async (invoke, method, path) => {
    await invoke();
    expect(mocks.adapter.mock.calls[0][0]).toMatchObject({ method, url: `http://localhost:9000/api/core${path}` });
  });

  it("retains the inactive-plan shape and request cancellation options", async () => {
    const controller = new AbortController();
    expect(await fetchCloudTokenPlan(controller.signal)).toEqual({ status: "inactive", modelQuotas: [], usage: [] });
    expect(mocks.adapter.mock.calls[0][0]).toMatchObject({ url: "http://localhost:9000/api/core/cloud/token-plan", signal: controller.signal, silentError: true });
  });

  it.each(["skill", "workflow"] as const)("pins %s file reads with If-Match and encodes IDs and file paths", async (kind) => {
    await getCloudResource(kind, "id/part");
    await getCloudResourceTree(kind, "id/part");
    await getCloudResourceContent(kind, "id/part", "docs/a b.md", "content-hash");
    await downloadCloudResource(kind, "id/part");
    const base = `http://localhost:9000/api/core/cloud/${kind === "skill" ? "skills" : "workflows"}/id%2Fpart`;
    expect(mocks.adapter.mock.calls[0][0].url).toBe(base);
    expect(mocks.adapter.mock.calls[1][0].url).toBe(`${base}/tree`);
    const request = mocks.adapter.mock.calls[2][0];
    expect(new URL(request.url).searchParams.get("path")).toBe("docs/a b.md");
    expect(request.headers.get("If-Match")).toBe('"content-hash"');
    expect(request.silentError).toBe(true);
    expect(mocks.adapter.mock.calls[3][0]).toMatchObject({ method: "post", url: `${base}:download` });
  });

  it("keeps upload on the skill action endpoint", async () => {
    await uploadCloudSkill("skill/id");
    expect(mocks.adapter.mock.calls[0][0]).toMatchObject({ method: "post", url: "http://localhost:9000/api/core/cloud/skills/skill%2Fid:upload" });
  });

  it("serializes restore mode and conflict resolution and encodes operation IDs", async () => {
    await startCredentialRestore("temporary", [{ recordId: "record", revision: 3, updatedAt: "now" }], "save_copy");
    const start = mocks.adapter.mock.calls[0][0];
    expect(start.url).toBe("http://localhost:9000/api/core/credential-vault/restores");
    expect(JSON.parse(start.data)).toEqual({ mode: "temporary", records: [{ record_id: "record", revision: 3, resolution: "save_copy" }] });
    expect(start.headers.get("Content-Type")).toBe("application/json");
    await getCredentialRestoreOperation("op/id");
    await cancelCredentialRestore("op/id");
    expect(mocks.adapter.mock.calls[1][0]).toMatchObject({ method: "get", url: "http://localhost:9000/api/core/credential-vault/restores/op%2Fid" });
    expect(mocks.adapter.mock.calls[2][0]).toMatchObject({ method: "delete", url: "http://localhost:9000/api/core/credential-vault/restores/op%2Fid" });
  });
});
