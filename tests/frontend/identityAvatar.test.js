import { beforeEach, describe, expect, it, vi } from "vitest";

const avatarApiMocks = vi.hoisted(() => ({
  deleteIdentityAvatar: vi.fn(),
  getIdentityAvatar: vi.fn(),
  putIdentityAvatar: vi.fn(),
}));

const authState = vi.hoisted(() => ({
  user: { userId: "user-1", username: "user-1" },
}));

vi.mock("@/modules/identityAvatar/api", () => ({
  deleteIdentityAvatar: avatarApiMocks.deleteIdentityAvatar,
  getIdentityAvatar: avatarApiMocks.getIdentityAvatar,
  putIdentityAvatar: avatarApiMocks.putIdentityAvatar,
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: () => authState.user,
  },
  AUTH_USER_CHANGE_EVENT: "lazymind:user-change",
}));

import {
  IDENTITY_AVATAR_MAX_BYTES,
  IdentityAvatarValidationError,
  validateIdentityAvatarFile,
} from "../../frontend/src/modules/identityAvatar/validation.ts";
import { useIdentityAvatarStore } from "../../frontend/src/modules/identityAvatar/store.ts";

let nextUrl = 0;

beforeEach(() => {
  nextUrl = 0;
  authState.user = { userId: "user-1", username: "user-1" };
  avatarApiMocks.deleteIdentityAvatar.mockReset();
  avatarApiMocks.getIdentityAvatar.mockReset();
  avatarApiMocks.putIdentityAvatar.mockReset();
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    value: vi.fn(() => `blob:avatar-${++nextUrl}`),
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    configurable: true,
    value: vi.fn(),
  });
  useIdentityAvatarStore.getState().reset();
});

describe("identity avatar file validation", () => {
  it("accepts PNG, JPEG, and WebP at the 2 MiB boundary", () => {
    for (const type of ["image/png", "image/jpeg", "image/webp"]) {
      expect(() =>
        validateIdentityAvatarFile(
          new File([new Uint8Array(IDENTITY_AVATAR_MAX_BYTES)], "avatar", {
            type,
          }),
        ),
      ).not.toThrow();
    }
  });

  it("rejects empty, unsupported, and oversized files", () => {
    const cases = [
      [new File([], "empty.png", { type: "image/png" }), "empty"],
      [new File(["svg"], "avatar.svg", { type: "image/svg+xml" }), "type"],
      [
        new File(
          [new Uint8Array(IDENTITY_AVATAR_MAX_BYTES + 1)],
          "large.png",
          { type: "image/png" },
        ),
        "size",
      ],
    ];

    for (const [file, reason] of cases) {
      try {
        validateIdentityAvatarFile(file);
        throw new Error("expected validation to fail");
      } catch (error) {
        expect(error).toBeInstanceOf(IdentityAvatarValidationError);
        expect(error.reason).toBe(reason);
      }
    }
  });
});

describe("identity avatar store", () => {
  it("deduplicates concurrent reads and keeps Soul/Profile independent", async () => {
    let resolveRequest;
    avatarApiMocks.getIdentityAvatar.mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve;
      }),
    );

    const first = useIdentityAvatarStore.getState().load("soul");
    const second = useIdentityAvatarStore.getState().load("soul");
    expect(avatarApiMocks.getIdentityAvatar).toHaveBeenCalledTimes(1);
    expect(useIdentityAvatarStore.getState().avatars.profile.status).toBe(
      "idle",
    );

    resolveRequest(new Blob(["agent"], { type: "image/png" }));
    await Promise.all([first, second]);
    expect(useIdentityAvatarStore.getState().avatars.soul).toMatchObject({
      status: "ready",
      url: "blob:avatar-1",
    });
  });

  it("maps 404 to missing and other read failures to error", async () => {
    avatarApiMocks.getIdentityAvatar.mockRejectedValueOnce({
      isAxiosError: true,
      response: { status: 404 },
    });
    await useIdentityAvatarStore.getState().load("profile");
    expect(useIdentityAvatarStore.getState().avatars.profile.status).toBe(
      "missing",
    );

    avatarApiMocks.getIdentityAvatar.mockRejectedValueOnce(new Error("down"));
    await useIdentityAvatarStore.getState().load("soul");
    expect(useIdentityAvatarStore.getState().avatars.soul.status).toBe("error");
  });

  it("replaces and revokes the previous URL after a successful upload", async () => {
    avatarApiMocks.getIdentityAvatar.mockResolvedValue(
      new Blob(["old"], { type: "image/png" }),
    );
    await useIdentityAvatarStore.getState().load("soul");

    avatarApiMocks.putIdentityAvatar.mockResolvedValue(undefined);
    const nextFile = new File(["new"], "avatar.png", { type: "image/png" });
    await useIdentityAvatarStore.getState().upload("soul", nextFile);

    expect(avatarApiMocks.putIdentityAvatar).toHaveBeenCalledWith(
      "soul",
      nextFile,
    );
    expect(useIdentityAvatarStore.getState().avatars.soul.url).toBe(
      "blob:avatar-2",
    );
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:avatar-1");
  });

  it("retains the previous avatar when upload fails", async () => {
    avatarApiMocks.getIdentityAvatar.mockResolvedValue(
      new Blob(["old"], { type: "image/png" }),
    );
    await useIdentityAvatarStore.getState().load("profile");
    avatarApiMocks.putIdentityAvatar.mockRejectedValue(new Error("failed"));

    await expect(
      useIdentityAvatarStore
        .getState()
        .upload(
          "profile",
          new File(["new"], "avatar.webp", { type: "image/webp" }),
        ),
    ).rejects.toThrow("failed");
    expect(useIdentityAvatarStore.getState().avatars.profile).toMatchObject({
      status: "ready",
      url: "blob:avatar-1",
    });
  });

  it("switches to the default icon after delete", async () => {
    avatarApiMocks.getIdentityAvatar.mockResolvedValue(
      new Blob(["old"], { type: "image/png" }),
    );
    await useIdentityAvatarStore.getState().load("profile");
    avatarApiMocks.deleteIdentityAvatar.mockResolvedValue(undefined);

    await useIdentityAvatarStore.getState().remove("profile");
    expect(useIdentityAvatarStore.getState().avatars.profile).toEqual({
      status: "missing",
    });
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:avatar-1");
  });

  it("clears both avatars and revokes URLs when the authenticated user changes", async () => {
    avatarApiMocks.getIdentityAvatar.mockResolvedValue(
      new Blob(["old"], { type: "image/png" }),
    );
    await useIdentityAvatarStore.getState().load("soul");

    authState.user = { userId: "user-2", username: "user-2" };
    useIdentityAvatarStore.getState().syncUser();

    expect(useIdentityAvatarStore.getState().userId).toBe("user-2");
    expect(useIdentityAvatarStore.getState().avatars).toEqual({
      soul: { status: "idle" },
      profile: { status: "idle" },
    });
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:avatar-1");
  });
});
