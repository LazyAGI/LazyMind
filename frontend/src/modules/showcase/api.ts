import {
  Configuration,
  ShowcaseApiFactory,
  type ShowcaseCase,
  type ShowcaseCaseListResponse,
  type ShowcaseCaseResult,
  type ShowcaseCaseTask,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { RawAxiosRequestConfig } from "axios";

const showcaseApi = ShowcaseApiFactory(
  new Configuration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

export type {
  ShowcaseCase,
  ShowcaseCaseListResponse,
  ShowcaseCaseResult,
  ShowcaseCaseTask,
};

export type ShowcaseEntryType = "chat" | "work";

export function matchesShowcaseEntryType(
  capabilityType: string,
  entryType: ShowcaseEntryType,
) {
  return entryType === "chat"
    ? capabilityType === "chat"
    : capabilityType === "work" || capabilityType === "workflow";
}

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
  return response.data;
}

export async function getShowcaseCase(
  caseId: string,
  options?: RawAxiosRequestConfig,
): Promise<ShowcaseCase> {
  const response = await showcaseApi.apiCoreShowcaseCasesCaseIdGet(
    { caseId },
    options,
  );
  return response.data;
}
