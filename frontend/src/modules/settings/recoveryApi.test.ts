import type { InternalAxiosRequestConfig } from "axios";
import { beforeEach, expect, it, vi } from "vitest";
import { archiveConversation, unarchiveConversation } from "./recoveryApi";

const mocks = vi.hoisted(() => ({ adapter: vi.fn() }));
vi.mock("@/components/request", async () => {
  const { default: axios } = await import("axios");
  return { BASE_URL: "http://recovery.test", axiosInstance: axios.create({ adapter: mocks.adapter }) };
});
vi.mock("@/modules/memory/skillApi", () => ({
  emptySkillTrash: vi.fn(), listTrashedSkillAssetsPage: vi.fn(), purgeSkillAsset: vi.fn(), restoreSkillAsset: vi.fn(),
}));

beforeEach(() => {
  mocks.adapter.mockReset().mockImplementation(async (config: InternalAxiosRequestConfig) => ({
    status: 200, statusText: "OK", config, headers: {},
    data: { conversation_id: "c", newly_archived: true, archived_at: "2026-10-19T08:00:00.123456Z" },
  }));
});

it("requests an active-only batch archive and preserves the exact server version", async () => {
  const result = await archiveConversation("c", "folder", true);
  const config = mocks.adapter.mock.calls[0][0];
  expect(config.url).toContain("/conversations/c:archive");
  expect(JSON.parse(config.data)).toEqual({ folder_id: "folder", only_if_active: true });
  expect(result.archived_at).toBe("2026-10-19T08:00:00.123456Z");
});

it("adds a silent conditional undo body while preserving ordinary manual restore", async () => {
  await unarchiveConversation("c", "2026-10-19T08:00:00.123456Z");
  const conditional = mocks.adapter.mock.calls[0][0];
  expect(JSON.parse(conditional.data)).toEqual({ expected_archived_at: "2026-10-19T08:00:00.123456Z" });
  expect(conditional.silentError).toBe(true);
  await unarchiveConversation("c");
  const manual = mocks.adapter.mock.calls[1][0];
  expect(JSON.parse(manual.data)).toEqual({});
  expect(manual.silentError).toBeUndefined();
  await archiveConversation("c", null);
  expect(JSON.parse(mocks.adapter.mock.calls[2][0].data)).toEqual({ folder_id: null });
});
