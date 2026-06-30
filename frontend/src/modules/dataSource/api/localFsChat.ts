import { BASE_URL, axiosInstance } from "@/components/request";
import { unwrapApiData } from "./unwrap";

export interface LocalFSChatSetting {
  enabled: boolean;
}

interface ApiEnvelope<T> {
  data?: T;
}

const basePath = BASE_URL || window.location.origin;

export async function getLocalFSChatSetting() {
  const response = await axiosInstance.get<
    ApiEnvelope<LocalFSChatSetting> | LocalFSChatSetting
  >(`${basePath}/api/core/data-sources/local-fs-chat-setting`);
  return unwrapApiData<LocalFSChatSetting>(response.data);
}

export async function updateLocalFSChatSetting(enabled: boolean) {
  const response = await axiosInstance.put<
    ApiEnvelope<LocalFSChatSetting> | LocalFSChatSetting
  >(`${basePath}/api/core/data-sources/local-fs-chat-setting`, { enabled });
  return unwrapApiData<LocalFSChatSetting>(response.data);
}
