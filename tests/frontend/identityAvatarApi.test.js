import fs from "node:fs";
import { beforeEach, describe, expect, it, vi } from "vitest";

const generatedClient = vi.hoisted(() => ({
  apiCoreMemoryProfileAvatarDelete: vi.fn(),
  apiCoreMemoryProfileAvatarGet: vi.fn(),
  apiCoreMemoryProfileAvatarPut: vi.fn(),
  apiCoreMemorySoulAvatarDelete: vi.fn(),
  apiCoreMemorySoulAvatarGet: vi.fn(),
  apiCoreMemorySoulAvatarPut: vi.fn(),
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class Configuration {},
  DefaultApiFactory: () => generatedClient,
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {},
  BASE_URL: "/gateway",
}));

import {
  deleteIdentityAvatar,
  getIdentityAvatar,
  putIdentityAvatar,
} from "../../frontend/src/modules/identityAvatar/api.ts";

beforeEach(() => {
  Object.values(generatedClient).forEach((mock) => mock.mockReset());
});

describe("generated identity avatar API adapter", () => {
  it("reads both avatar kinds as blobs through generated methods", async () => {
    const soul = new Blob(["soul"], { type: "image/png" });
    const profile = new Blob(["profile"], { type: "image/webp" });
    generatedClient.apiCoreMemorySoulAvatarGet.mockResolvedValue({
      data: soul,
      headers: { "content-type": "image/png" },
    });
    generatedClient.apiCoreMemoryProfileAvatarGet.mockResolvedValue({
      data: profile,
      headers: { "content-type": "image/webp" },
    });

    await expect(getIdentityAvatar("soul")).resolves.toBe(soul);
    await expect(getIdentityAvatar("profile")).resolves.toBe(profile);
    expect(generatedClient.apiCoreMemorySoulAvatarGet).toHaveBeenCalledWith({
      responseType: "blob",
      silentError: true,
    });
    expect(generatedClient.apiCoreMemoryProfileAvatarGet).toHaveBeenCalledWith({
      responseType: "blob",
      silentError: true,
    });
  });

  it("uploads and deletes through the generated Soul/Profile operations", async () => {
    const soul = new File(["soul"], "soul.png", { type: "image/png" });
    const profile = new File(["profile"], "profile.jpg", {
      type: "image/jpeg",
    });
    generatedClient.apiCoreMemorySoulAvatarPut.mockResolvedValue({});
    generatedClient.apiCoreMemoryProfileAvatarPut.mockResolvedValue({});
    generatedClient.apiCoreMemorySoulAvatarDelete.mockResolvedValue({});
    generatedClient.apiCoreMemoryProfileAvatarDelete.mockResolvedValue({});

    await putIdentityAvatar("soul", soul);
    await putIdentityAvatar("profile", profile);
    await deleteIdentityAvatar("soul");
    await deleteIdentityAvatar("profile");

    expect(generatedClient.apiCoreMemorySoulAvatarPut).toHaveBeenCalledWith({
      file: soul,
    });
    expect(generatedClient.apiCoreMemoryProfileAvatarPut).toHaveBeenCalledWith({
      file: profile,
    });
    expect(
      generatedClient.apiCoreMemorySoulAvatarDelete,
    ).toHaveBeenCalledWith();
    expect(
      generatedClient.apiCoreMemoryProfileAvatarDelete,
    ).toHaveBeenCalledWith();
  });
});

describe("chat avatar integration", () => {
  it("uses the shared avatar component for both assistant render modes and user messages", () => {
    const assistantSource = fs.readFileSync(
      new URL(
        "../../frontend/src/modules/chat/components/AssistantMessage/index.tsx",
        import.meta.url,
      ),
      "utf8",
    );
    const messageListSource = fs.readFileSync(
      new URL(
        "../../frontend/src/modules/chat/components/newChatContainer/components/MessageList.tsx",
        import.meta.url,
      ),
      "utf8",
    );

    expect(assistantSource.match(/<IdentityAvatar/g)).toHaveLength(2);
    expect(assistantSource).toContain('kind="soul"');
    expect(assistantSource).not.toContain("bot_avatar.png");
    expect(messageListSource).toContain('kind="profile"');
    expect(messageListSource).toContain("chat-user-bubble-row");
  });
});
