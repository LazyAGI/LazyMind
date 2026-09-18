import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useCloudResourceTable } from "./useCloudResourceTable";
import type { CloudResourceItem } from "../cloudResourceApi";

const mocks = vi.hoisted(() => ({ session: vi.fn(), list: vi.fn(), download: vi.fn() }));
vi.mock("@/runtime/cloud/session", () => ({ getCloudSession: mocks.session, isCloudBusinessAvailable: () => true }));
vi.mock("@/runtime/desktopBridge", () => ({ openCloudRegister: vi.fn() }));
vi.mock("../cloudResourceApi", () => ({ listCloudResources: mocks.list, downloadCloudResource: mocks.download }));
vi.mock("antd", () => ({ message: { success: vi.fn(), error: vi.fn() } }));
const t = (key: string) => key;
const item: CloudResourceItem = {
  resource_id: "skill", resource_type: "skill", resource_name: "Skill", content_size: 10,
  format_schema: "lazymind.resource-manifest/v2", updated_at: "2026-09-01T00:00:00Z",
  presence_status: "download_required", local_exists: false,
};
beforeEach(() => {
  mocks.session.mockReset().mockResolvedValue({});
  mocks.list.mockReset().mockResolvedValue([item]);
  mocks.download.mockReset().mockResolvedValue({});
});
afterEach(cleanup);

describe("Cloud resource table actions", () => {
  it("does not overwrite a refreshed list with an older response", async () => {
    let finish!: (value: CloudResourceItem[]) => void;
    mocks.list.mockReturnValueOnce(new Promise((resolve) => { finish = resolve }));
    const { result, rerender } = renderHook(({ refreshKey }) => useCloudResourceTable({ resourceType: "skill", t, refreshKey }), { initialProps: { refreshKey: 0 } });
    await waitFor(() => expect(mocks.list).toHaveBeenCalledOnce());
    rerender({ refreshKey: 1 });
    await waitFor(() => expect(result.current.items).toEqual([item]));
    await act(async () => finish([{ ...item, resource_id: "stale" }]));
    expect(result.current.items).toEqual([item]);
  });

  it("refreshes the local list before reloading Cloud presence after a download", async () => {
    const onDownloaded = vi.fn().mockResolvedValue(undefined);
    const { result } = renderHook(() => useCloudResourceTable({ resourceType: "skill", t, onDownloaded }));
    await waitFor(() => expect(result.current.items).toEqual([item]));
    mocks.list.mockClear().mockResolvedValue([{ ...item, presence_status: "present_current", local_exists: true }]);
    await act(async () => result.current.handleDownload(item));
    expect(mocks.download).toHaveBeenCalledWith("skill", "skill");
    expect(onDownloaded.mock.invocationCallOrder[0]).toBeLessThan(mocks.list.mock.invocationCallOrder[0]);
    expect(result.current.items[0].presence_status).toBe("present_current");
    expect(result.current.downloading.size).toBe(0);
  });
});
