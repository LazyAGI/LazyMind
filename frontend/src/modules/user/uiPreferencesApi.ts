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

function unwrapUiPreferencesData<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

export async function fetchUserUiPreferences(): Promise<UserUIPreferencesOpenAPIResponse> {
  const response = await userApi.apiCoreUserUiPreferencesGet();
  return unwrapUiPreferencesData<UserUIPreferencesOpenAPIResponse>(response.data);
}

export async function patchUserUiPreferences(
  patch: UserUIPreferencesPatchOpenAPIRequest,
): Promise<UserUIPreferencesOpenAPIResponse> {
  const response = await userApi.apiCoreUserUiPreferencesPatch({
    userUIPreferencesPatchOpenAPIRequest: patch,
  });
  return unwrapUiPreferencesData<UserUIPreferencesOpenAPIResponse>(response.data);
}
