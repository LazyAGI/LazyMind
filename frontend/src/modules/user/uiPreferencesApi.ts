import {
  Configuration,
  UserApiFactory,
  type UserUIPreferencesOpenAPIResponse,
  type UserUIPreferencesPatchOpenAPIRequest,
} from "@/api/generated/core-client";
import { BASE_URL, axiosInstance } from "@/components/request";

interface ApiEnvelope<T> {
  data?: T;
}

const coreConfig = new Configuration({ basePath: BASE_URL });

const userApi = UserApiFactory(coreConfig, BASE_URL, axiosInstance);

export const USER_UI_PREFERENCES_CHANGED_EVENT = "lazymind:user-ui-preferences-changed";

function unwrapUiPreferencesData<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

export async function fetchUserUiPreferences(
  options?: Parameters<typeof userApi.apiCoreUserUiPreferencesGet>[0],
): Promise<UserUIPreferencesOpenAPIResponse> {
  const response = await userApi.apiCoreUserUiPreferencesGet(options);
  return unwrapUiPreferencesData<UserUIPreferencesOpenAPIResponse>(response.data);
}

export async function patchUserUiPreferences(
  patch: UserUIPreferencesPatchOpenAPIRequest,
): Promise<UserUIPreferencesOpenAPIResponse> {
  const response = await userApi.apiCoreUserUiPreferencesPatch({
    userUIPreferencesPatchOpenAPIRequest: patch,
  });
  const preferences = unwrapUiPreferencesData<UserUIPreferencesOpenAPIResponse>(response.data);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(USER_UI_PREFERENCES_CHANGED_EVENT, {
      detail: preferences,
    }));
  }
  return preferences;
}
