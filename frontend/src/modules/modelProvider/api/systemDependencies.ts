import { BASE_URL, axiosInstance } from "@/components/request";
import { unwrapApiData } from "@/modules/dataSource/api/unwrap";

export type FFmpegDependencySource = "custom" | "bundled" | "system" | "auto";

export interface FFmpegDependencyStatus {
  installed: boolean;
  source: FFmpegDependencySource | string;
  ffmpegPath?: string;
  ffprobePath?: string;
  customPath?: string;
  bundledBinDir?: string;
  affectedFeatures: string[];
  runtimeLocal: boolean;
  installSupported: boolean;
  message?: string;
}

export interface EditablePPTDependencyStatus {
  installed: boolean;
  installDir?: string;
  chromiumPath?: string;
  affectedFeatures: string[];
  runtimeLocal: boolean;
  installSupported: boolean;
  message?: string;
}

export interface BrowserExtensionDependencyStatus {
  installed: boolean;
  installDir?: string;
  manifestPath?: string;
  version?: string;
  availableVersion?: string;
  updateAvailable: boolean;
  affectedFeatures: string[];
  runtimeLocal: boolean;
  installSupported: boolean;
  browserApprovalRequired: boolean;
  browserSettingsUrl: string;
  supportedBrowsers?: BrowserExtensionTarget[];
  message?: string;
}

export interface BrowserExtensionTarget {
  id: "chrome" | "edge" | string;
  name: string;
  settingsUrl: string;
}

export interface BrowserPairingCode {
  code: string;
  expires_at: string;
}

export interface BrowserDeviceInfo {
  id: string;
  name: string;
  browser: string;
  browser_version?: string;
  extension_version?: string;
  version?: string;
  online: boolean;
  created_at: string;
  last_seen_at: string;
}

interface ApiEnvelope<T> {
  data?: T;
}

const basePath = BASE_URL || window.location.origin;

export async function getFFmpegDependencyStatus() {
  const response = await axiosInstance.get<
    ApiEnvelope<FFmpegDependencyStatus> | FFmpegDependencyStatus
  >(`${basePath}/api/core/system-dependencies/ffmpeg`);
  return unwrapApiData<FFmpegDependencyStatus>(response.data);
}

export async function updateFFmpegDependency(payload: {
  source: "custom" | "bundled";
  customPath?: string;
}) {
  const response = await axiosInstance.put<
    ApiEnvelope<FFmpegDependencyStatus> | FFmpegDependencyStatus
  >(`${basePath}/api/core/system-dependencies/ffmpeg`, payload);
  return unwrapApiData<FFmpegDependencyStatus>(response.data);
}

export async function checkFFmpegDependency() {
  const response = await axiosInstance.post<
    ApiEnvelope<FFmpegDependencyStatus> | FFmpegDependencyStatus
  >(`${basePath}/api/core/system-dependencies/ffmpeg:check`);
  return unwrapApiData<FFmpegDependencyStatus>(response.data);
}

export async function installFFmpegDependency() {
  const response = await axiosInstance.post<
    ApiEnvelope<FFmpegDependencyStatus> | FFmpegDependencyStatus
  >(
    `${basePath}/api/core/system-dependencies/ffmpeg:install`,
    undefined,
    { timeout: 30 * 60 * 1000 },
  );
  return unwrapApiData<FFmpegDependencyStatus>(response.data);
}

export async function getEditablePPTDependencyStatus() {
  const response = await axiosInstance.get<
    ApiEnvelope<EditablePPTDependencyStatus> | EditablePPTDependencyStatus
  >(`${basePath}/api/core/system-dependencies/editable-ppt`);
  return unwrapApiData<EditablePPTDependencyStatus>(response.data);
}

export async function checkEditablePPTDependency() {
  const response = await axiosInstance.post<
    ApiEnvelope<EditablePPTDependencyStatus> | EditablePPTDependencyStatus
  >(`${basePath}/api/core/system-dependencies/editable-ppt:check`);
  return unwrapApiData<EditablePPTDependencyStatus>(response.data);
}

export async function installEditablePPTDependency() {
  const response = await axiosInstance.post<
    ApiEnvelope<EditablePPTDependencyStatus> | EditablePPTDependencyStatus
  >(
    `${basePath}/api/core/system-dependencies/editable-ppt:install`,
    undefined,
    { timeout: 45 * 60 * 1000 },
  );
  return unwrapApiData<EditablePPTDependencyStatus>(response.data);
}

export async function getBrowserExtensionDependencyStatus() {
  const response = await axiosInstance.get<
    ApiEnvelope<BrowserExtensionDependencyStatus> | BrowserExtensionDependencyStatus
  >(`${basePath}/api/core/system-dependencies/browser-extension`);
  return unwrapApiData<BrowserExtensionDependencyStatus>(response.data);
}

export async function checkBrowserExtensionDependency() {
  const response = await axiosInstance.post<
    ApiEnvelope<BrowserExtensionDependencyStatus> | BrowserExtensionDependencyStatus
  >(`${basePath}/api/core/system-dependencies/browser-extension:check`);
  return unwrapApiData<BrowserExtensionDependencyStatus>(response.data);
}

export async function installBrowserExtensionDependency() {
  const response = await axiosInstance.post<
    ApiEnvelope<BrowserExtensionDependencyStatus> | BrowserExtensionDependencyStatus
  >(
    `${basePath}/api/core/system-dependencies/browser-extension:install`,
    undefined,
    { timeout: 10 * 60 * 1000 },
  );
  return unwrapApiData<BrowserExtensionDependencyStatus>(response.data);
}

export async function createBrowserPairingCode() {
  const response = await axiosInstance.post<
    ApiEnvelope<BrowserPairingCode> | BrowserPairingCode
  >(`${basePath}/api/core/browser/manage/pairings`);
  return unwrapApiData<BrowserPairingCode>(response.data);
}

export async function getBrowserDevices() {
  const response = await axiosInstance.get<
    ApiEnvelope<{ devices: BrowserDeviceInfo[] }> | { devices: BrowserDeviceInfo[] }
  >(`${basePath}/api/core/browser/manage/devices`);
  const result = unwrapApiData<{ devices: BrowserDeviceInfo[] }>(response.data);
  return result.devices || [];
}

export async function revokeBrowserDevice(deviceID: string) {
  await axiosInstance.delete(
    `${basePath}/api/core/browser/manage/devices/${encodeURIComponent(deviceID)}`,
  );
}
