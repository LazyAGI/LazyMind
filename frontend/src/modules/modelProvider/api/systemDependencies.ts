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
