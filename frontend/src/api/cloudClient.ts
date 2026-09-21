import { Configuration, DefaultApiFactory, DesktopCloudApiFactory } from "./generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { RawAxiosRequestConfig } from "axios";

const configuration = new Configuration({ basePath: BASE_URL });

export const cloudApi = DefaultApiFactory(configuration, BASE_URL, axiosInstance);
export const cloudResourceApi = DesktopCloudApiFactory(configuration, BASE_URL, axiosInstance);
export const silentCloudRequest: RawAxiosRequestConfig & { silentError: boolean } = { silentError: true };
