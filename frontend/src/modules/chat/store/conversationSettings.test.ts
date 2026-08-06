import { beforeEach, describe, expect, it, vi } from "vitest";

const mockGetStatus = vi.fn();

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceGetMultiAnswersSwitchStatus: mockGetStatus,
  }),
}));

import { useConversationSettings } from "./conversationSettings";

describe("useConversationSettings", () => {
  beforeEach(() => {
    mockGetStatus.mockReset();
    useConversationSettings.setState({
      enableMultipleAnswers: false,
      isLoading: false,
    });
  });

  it("setEnableMultipleAnswers updates the flag", () => {
    useConversationSettings.getState().setEnableMultipleAnswers(true);
    expect(useConversationSettings.getState().enableMultipleAnswers).toBe(true);
  });

  it("fetchSwitchStatus sets enableMultipleAnswers based on status === 1", async () => {
    mockGetStatus.mockResolvedValueOnce({ data: { status: 1 } });

    await useConversationSettings.getState().fetchSwitchStatus();

    expect(useConversationSettings.getState().enableMultipleAnswers).toBe(true);
    expect(useConversationSettings.getState().isLoading).toBe(false);
  });

  it("fetchSwitchStatus sets enableMultipleAnswers to false for any other status", async () => {
    mockGetStatus.mockResolvedValueOnce({ data: { status: 0 } });

    await useConversationSettings.getState().fetchSwitchStatus();

    expect(useConversationSettings.getState().enableMultipleAnswers).toBe(false);
  });

  it("fetchSwitchStatus resets isLoading and swallows errors on failure", async () => {
    mockGetStatus.mockRejectedValueOnce(new Error("network error"));

    await useConversationSettings.getState().fetchSwitchStatus();

    expect(useConversationSettings.getState().isLoading).toBe(false);
  });

  it("fetchSwitchStatus is a no-op while already loading", async () => {
    useConversationSettings.setState({ isLoading: true });

    await useConversationSettings.getState().fetchSwitchStatus();

    expect(mockGetStatus).not.toHaveBeenCalled();
  });
});
