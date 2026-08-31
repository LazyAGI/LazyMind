import type { RawAxiosRequestConfig } from "axios";

import {
  Configuration,
  KnowledgeMarketApiFactory,
  type KnowledgeMarketDetailOpenAPIResponse,
  type KnowledgeMarketDomainsOpenAPIResponse,
  type KnowledgeMarketInstallOpenAPIResponse,
  type KnowledgeMarketInstallsOpenAPIResponse,
  type KnowledgeMarketListItemOpenAPIResponse,
  type KnowledgeMarketTaskDetailOpenAPIResponse,
  type KnowledgeMarketTaskListOpenAPIResponse,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";

const knowledgeMarketClient = KnowledgeMarketApiFactory(
  new Configuration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

interface CoreEnvelope<T> {
  data?: T;
}

type KnowledgeMarketRequestOptions = RawAxiosRequestConfig & {
  silentError?: boolean;
};

export type KnowledgeMarketDetail = KnowledgeMarketDetailOpenAPIResponse & {
  online_access_url?: string;
};

function unwrap<T>(payload: T | CoreEnvelope<T>): T {
  if (
    payload &&
    typeof payload === "object" &&
    Object.prototype.hasOwnProperty.call(payload, "data")
  ) {
    return (payload as CoreEnvelope<T>).data as T;
  }
  return payload as T;
}

export async function listKnowledgeMarket(
  options?: KnowledgeMarketRequestOptions,
): Promise<KnowledgeMarketListItemOpenAPIResponse[]> {
  const pageSize = 100;
  const firstResponse = await knowledgeMarketClient.apiCoreKnowledgeMarketGet(
    { page: 1, pageSize },
    options,
  );
  const first = unwrap(firstResponse.data);
  const items = [...(first.items || [])];
  const pageCount = Math.ceil(first.total / pageSize);

  if (pageCount <= 1) return items;

  const remaining = await Promise.all(
    Array.from({ length: pageCount - 1 }, (_, index) =>
      knowledgeMarketClient
        .apiCoreKnowledgeMarketGet(
          { page: index + 2, pageSize },
          options,
        )
        .then((response) => unwrap(response.data)),
    ),
  );
  remaining.forEach((page) => items.push(...(page.items || [])));
  return items;
}

export async function listKnowledgeMarketDomains(
  options?: KnowledgeMarketRequestOptions,
): Promise<KnowledgeMarketDomainsOpenAPIResponse> {
  const response = await knowledgeMarketClient.apiCoreKnowledgeMarketDomainsGet(
    options,
  );
  return unwrap(response.data);
}

export async function listKnowledgeMarketInstalls(
  options?: KnowledgeMarketRequestOptions,
): Promise<KnowledgeMarketInstallsOpenAPIResponse> {
  const response = await knowledgeMarketClient.apiCoreKnowledgeMarketInstallsGet(
    options,
  );
  return unwrap(response.data);
}

export async function getKnowledgeMarketItem(
  marketItemId: string,
  options?: KnowledgeMarketRequestOptions,
): Promise<KnowledgeMarketDetail> {
  const response =
    await knowledgeMarketClient.apiCoreKnowledgeMarketItemsMarketItemIdGet(
      { marketItemId },
      options,
    );
  return unwrap(response.data) as KnowledgeMarketDetail;
}

export async function installKnowledgeMarketItem(
  marketItemId: string,
): Promise<KnowledgeMarketInstallOpenAPIResponse> {
  const response =
    await knowledgeMarketClient.apiCoreKnowledgeMarketItemsMarketItemIdInstallPost(
      { marketItemId },
    );
  return unwrap(response.data);
}

export async function updateKnowledgeMarketItem(
  marketItemId: string,
): Promise<KnowledgeMarketInstallOpenAPIResponse> {
  const response =
    await knowledgeMarketClient.apiCoreKnowledgeMarketItemsMarketItemIdUpdatePost(
      { marketItemId },
    );
  return unwrap(response.data);
}

export async function updateAllKnowledgeMarketItems(): Promise<KnowledgeMarketInstallOpenAPIResponse> {
  const response =
    await knowledgeMarketClient.apiCoreKnowledgeMarketUpdateAllPost();
  return unwrap(response.data);
}

export async function listKnowledgeMarketTasks(
  jobType: string,
  options?: KnowledgeMarketRequestOptions,
): Promise<KnowledgeMarketTaskListOpenAPIResponse> {
  const response = await knowledgeMarketClient.apiCoreKnowledgeMarketTasksGet(
    { page: 1, pageSize: 20, jobType },
    options,
  );
  return unwrap(response.data);
}

export async function getKnowledgeMarketTask(
  jobId: string,
  options?: KnowledgeMarketRequestOptions,
): Promise<KnowledgeMarketTaskDetailOpenAPIResponse> {
  const response =
    await knowledgeMarketClient.apiCoreKnowledgeMarketTasksJobIdGet(
      { jobId },
      options,
    );
  return unwrap(response.data);
}
