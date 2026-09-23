import { beforeEach, describe, expect, it, vi } from "vitest";
import { applySettingsChange, type SettingsChangeKey } from "./api";

const api = vi.hoisted(() => ({ patchPreferences: vi.fn(), setMcpEnabled: vi.fn() }));
vi.mock("@/modules/user/uiPreferencesApi", () => ({ patchUserUiPreferences: api.patchPreferences }));
vi.mock("@/modules/memory/toolApi", () => ({ setAllMcpServersEnabled: api.setMcpEnabled }));
vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));

describe("settings persistence", () => {
  beforeEach(() => vi.resetAllMocks());

  it.each<SettingsChangeKey>([
    "developer_mode_active", "task_center_enabled", "schedules_enabled",
    "skills_enabled", "workflows_enabled", "document_parsing_enabled",
  ])("saves %s through the existing preferences API", async (key) => {
    for (const enabled of [false, true]) {
      const preferences = { [key]: enabled };
      api.patchPreferences.mockResolvedValueOnce(preferences);
      expect(await applySettingsChange({ key, enabled })).toEqual({ key, enabled, preferences });
      expect(api.patchPreferences).toHaveBeenLastCalledWith({ [key]: enabled });
    }
    expect(api.patchPreferences).toHaveBeenCalledTimes(2);
    expect(api.setMcpEnabled).not.toHaveBeenCalled();
  });

  it.each([false, true])("saves MCP enabled=%s through the existing bulk API and keeps verification counts", async (enabled) => {
    const mcp = { enabled, totalCount: 3, updatedCount: enabled ? 2 : 3, skippedUnverifiedCount: enabled ? 1 : 0 };
    api.setMcpEnabled.mockResolvedValueOnce(mcp);
    expect(await applySettingsChange({ key: "mcp_enabled", enabled })).toEqual({ key: "mcp_enabled", enabled, mcp });
    expect(api.setMcpEnabled).toHaveBeenCalledWith(enabled);
    expect(api.patchPreferences).not.toHaveBeenCalled();
  });

  it.each<SettingsChangeKey>(["skills_enabled", "mcp_enabled"])("reports failed saves for %s to the confirmation dialog", async (key) => {
    const error = new Error("offline");
    api.patchPreferences.mockRejectedValue(error);
    api.setMcpEnabled.mockRejectedValue(error);
    await expect(applySettingsChange({ key, enabled: false })).rejects.toBe(error);
  });
});
