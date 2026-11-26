import {
  Configuration,
  UserApiFactory,
  type UserUIPreferencesOpenAPIResponse,
  type UserUIPreferencesPatchOpenAPIRequest,
} from "@/api/generated/core-client";
import { BASE_URL, axiosInstance } from "@/components/request";
import { AgentAppsAuth } from "@/components/auth";

interface ApiEnvelope<T> {
  data?: T;
}

const coreConfig = new Configuration({ basePath: BASE_URL });

const userApi = UserApiFactory(coreConfig, BASE_URL, axiosInstance);

export const USER_UI_PREFERENCES_CHANGED_EVENT = "lazymind:user-ui-preferences-changed";
const pendingReads = new Map<string, Promise<UserUIPreferencesOpenAPIResponse>>();

function unwrapUiPreferencesData<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

export function fetchUserUiPreferences(
  options?: Parameters<typeof userApi.apiCoreUserUiPreferencesGet>[0],
): Promise<UserUIPreferencesOpenAPIResponse> {
  const read = () => userApi.apiCoreUserUiPreferencesGet(options).then(response =>
    unwrapUiPreferencesData<UserUIPreferencesOpenAPIResponse>(response.data));
  // Custom cancellation, headers and error handling belong to the caller.
  if (options) return read();
  const identity = AgentAppsAuth.getSessionIdentity();
  const existing = pendingReads.get(identity);
  if (existing) return existing;
  const pending = read().finally(() => {
    if (pendingReads.get(identity) === pending) pendingReads.delete(identity);
  });
  pendingReads.set(identity, pending);
  return pending;
}

export async function patchUserUiPreferences(
  patch: UserUIPreferencesPatchOpenAPIRequest,
): Promise<UserUIPreferencesOpenAPIResponse> {
  const identity = AgentAppsAuth.getSessionIdentity();
  pendingReads.delete(identity);
  const response = await userApi.apiCoreUserUiPreferencesPatch({
    userUIPreferencesPatchOpenAPIRequest: patch,
  });
  pendingReads.delete(identity);
  const preferences = unwrapUiPreferencesData<UserUIPreferencesOpenAPIResponse>(response.data);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(USER_UI_PREFERENCES_CHANGED_EVENT, {
      detail: preferences,
    }));
  }
  return preferences;
}
