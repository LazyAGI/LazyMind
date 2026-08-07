import axios from "axios";
import { create } from "zustand";
import {
  AgentAppsAuth,
  AUTH_USER_CHANGE_EVENT,
} from "@/components/auth";
import {
  deleteIdentityAvatar,
  getIdentityAvatar,
  type IdentityAvatarKind,
  putIdentityAvatar,
} from "./api";
import { validateIdentityAvatarFile } from "./validation";

export type IdentityAvatarStatus =
  | "idle"
  | "loading"
  | "ready"
  | "missing"
  | "error";

export interface IdentityAvatarEntry {
  status: IdentityAvatarStatus;
  url?: string;
  error?: unknown;
}

interface IdentityAvatarState {
  userId: string;
  avatars: Record<IdentityAvatarKind, IdentityAvatarEntry>;
  syncUser: () => string;
  load: (kind: IdentityAvatarKind, force?: boolean) => Promise<void>;
  upload: (kind: IdentityAvatarKind, file: File) => Promise<void>;
  remove: (kind: IdentityAvatarKind) => Promise<void>;
  markImageError: (kind: IdentityAvatarKind) => void;
  reset: () => void;
}

const emptyAvatar = (): IdentityAvatarEntry => ({ status: "idle" });
const emptyAvatars = (): Record<IdentityAvatarKind, IdentityAvatarEntry> => ({
  soul: emptyAvatar(),
  profile: emptyAvatar(),
});

const inflightLoads: Partial<
  Record<IdentityAvatarKind, { userId: string; promise: Promise<void> }>
> = {};

function authenticatedUserId(): string {
  const user = AgentAppsAuth.getUserInfo();
  return user?.userId || user?.username || "";
}

function revokeAvatar(entry: IdentityAvatarEntry | undefined) {
  if (entry?.url) {
    URL.revokeObjectURL(entry.url);
  }
}

function isMissingAvatar(error: unknown) {
  return axios.isAxiosError(error) && error.response?.status === 404;
}

export const useIdentityAvatarStore = create<IdentityAvatarState>(
  (set, get) => ({
    userId: "",
    avatars: emptyAvatars(),

    syncUser: () => {
      const nextUserId = authenticatedUserId();
      const state = get();
      if (state.userId !== nextUserId) {
        revokeAvatar(state.avatars.soul);
        revokeAvatar(state.avatars.profile);
        delete inflightLoads.soul;
        delete inflightLoads.profile;
        set({ userId: nextUserId, avatars: emptyAvatars() });
      }
      return nextUserId;
    },

    load: async (kind, force = false) => {
      const userId = get().syncUser();
      if (!userId) {
        return;
      }

      const entry = get().avatars[kind];
      if (!force && ["ready", "missing"].includes(entry.status)) {
        return;
      }
      const existing = inflightLoads[kind];
      if (existing?.userId === userId) {
        return existing.promise;
      }

      set((state) => ({
        avatars: {
          ...state.avatars,
          [kind]: { ...state.avatars[kind], status: "loading", error: undefined },
        },
      }));

      const promise = (async () => {
        try {
          const blob = await getIdentityAvatar(kind);
          if (get().userId !== userId) {
            return;
          }
          const url = URL.createObjectURL(blob);
          const previous = get().avatars[kind];
          set((state) => ({
            avatars: {
              ...state.avatars,
              [kind]: { status: "ready", url },
            },
          }));
          revokeAvatar(previous);
        } catch (error) {
          if (get().userId !== userId) {
            return;
          }
          const previous = get().avatars[kind];
          revokeAvatar(previous);
          set((state) => ({
            avatars: {
              ...state.avatars,
              [kind]: {
                status: isMissingAvatar(error) ? "missing" : "error",
                ...(!isMissingAvatar(error) ? { error } : {}),
              },
            },
          }));
        } finally {
          if (inflightLoads[kind]?.userId === userId) {
            delete inflightLoads[kind];
          }
        }
      })();

      inflightLoads[kind] = { userId, promise };
      return promise;
    },

    upload: async (kind, file) => {
      validateIdentityAvatarFile(file);
      const userId = get().syncUser();
      if (!userId) {
        return;
      }
      const previous = get().avatars[kind];
      set((state) => ({
        avatars: {
          ...state.avatars,
          [kind]: { ...previous, status: "loading", error: undefined },
        },
      }));

      try {
        await putIdentityAvatar(kind, file);
        if (get().userId !== userId) {
          return;
        }
        const url = URL.createObjectURL(file);
        set((state) => ({
          avatars: {
            ...state.avatars,
            [kind]: { status: "ready", url },
          },
        }));
        revokeAvatar(previous);
      } catch (error) {
        if (get().userId === userId) {
          set((state) => ({
            avatars: { ...state.avatars, [kind]: previous },
          }));
        }
        throw error;
      }
    },

    remove: async (kind) => {
      const userId = get().syncUser();
      if (!userId) {
        return;
      }
      const previous = get().avatars[kind];
      set((state) => ({
        avatars: {
          ...state.avatars,
          [kind]: { ...previous, status: "loading", error: undefined },
        },
      }));
      try {
        await deleteIdentityAvatar(kind);
        if (get().userId !== userId) {
          return;
        }
        set((state) => ({
          avatars: {
            ...state.avatars,
            [kind]: { status: "missing" },
          },
        }));
        revokeAvatar(previous);
      } catch (error) {
        if (get().userId === userId) {
          set((state) => ({
            avatars: { ...state.avatars, [kind]: previous },
          }));
        }
        throw error;
      }
    },

    markImageError: (kind) => {
      const previous = get().avatars[kind];
      revokeAvatar(previous);
      set((state) => ({
        avatars: {
          ...state.avatars,
          [kind]: { status: "error" },
        },
      }));
    },

    reset: () => {
      const state = get();
      revokeAvatar(state.avatars.soul);
      revokeAvatar(state.avatars.profile);
      delete inflightLoads.soul;
      delete inflightLoads.profile;
      set({ userId: authenticatedUserId(), avatars: emptyAvatars() });
    },
  }),
);

if (typeof window !== "undefined") {
  window.addEventListener(AUTH_USER_CHANGE_EVENT, () => {
    useIdentityAvatarStore.getState().syncUser();
  });
  window.addEventListener("beforeunload", () => {
    useIdentityAvatarStore.getState().reset();
  });
}
