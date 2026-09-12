import {
  Configuration,
  ShowcaseApiFactory,
  type ShowcaseCase as ApiShowcaseCase,
  type ShowcaseCaseListResponse as ApiShowcaseCaseListResponse,
  type ShowcaseCaseResult,
  type ShowcaseCaseTask,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { RawAxiosRequestConfig } from "axios";
export {
  matchesShowcaseEntryType,
  showcaseEntryType,
  showcaseTechnologyType,
  type ShowcaseEntryType,
  type ShowcaseTechnologyType,
} from "./classification";

const showcaseApi = ShowcaseApiFactory(
  new Configuration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

export type ShowcaseCase = Omit<ApiShowcaseCase, "tasks"> & { tasks: ShowcaseCaseTask[] };
export type ShowcaseCaseListResponse = Omit<ApiShowcaseCaseListResponse, "cases"> & { cases?: ShowcaseCase[] };

const normalizeCase = (item: ApiShowcaseCase): ShowcaseCase => ({ ...item, tasks: item.tasks ?? [] });

export type {
  ShowcaseCaseResult,
  ShowcaseCaseTask,
};

export async function listShowcaseCases(
  params: { keyword?: string; category?: string } = {},
  options?: RawAxiosRequestConfig,
): Promise<ShowcaseCaseListResponse> {
  const response = await showcaseApi.apiCoreShowcaseCasesGet(
    {
      keyword: params.keyword,
      category: params.category,
    },
    options,
  );
  return { ...response.data, cases: response.data.cases?.map(normalizeCase) };
}

export async function getShowcaseCase(
  caseId: string,
  options?: RawAxiosRequestConfig,
): Promise<ShowcaseCase> {
  const response = await showcaseApi.apiCoreShowcaseCasesCaseIdGet(
    { caseId },
    options,
  );
  return normalizeCase(response.data);
}
