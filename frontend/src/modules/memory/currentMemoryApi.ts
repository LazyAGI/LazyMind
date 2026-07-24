import {
  Configuration,
  DefaultApiFactory,
  type CurrentMemoryPreferenceItem,
  type CurrentMemoryPreferenceListData,
  type CurrentMemoryProfileDocument,
  type CurrentMemoryProfilePatchRequest,
  type CurrentMemoryReference,
  type CurrentMemorySoulDocument,
  type CurrentMemorySoulPatchRequest,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";

export type SoulDocument = CurrentMemorySoulDocument;
export type SoulPatch = CurrentMemorySoulPatchRequest;
export type ProfileDocument = CurrentMemoryProfileDocument;
export type ProfilePatch = CurrentMemoryProfilePatchRequest;

export interface CurrentMemorySnapshot<TDocument> {
  document: TDocument;
  updatedAt: number;
}

export interface PreferenceMemoryItem {
  name: string;
  summary: string;
  createdAt: string;
  updatedAt: string;
}

export interface PreferenceMemoryList {
  items: PreferenceMemoryItem[];
  etag: string;
  totalSize: number;
  updatedAt: number;
}

export interface PreferenceMemoryReference {
  name: string;
  summary: string;
  createdAt: string;
  updatedAt: string;
  source: {
    kind: CurrentMemoryReference["source"]["kind"];
    conversationId: string;
  };
  applicationScenarios: string;
  preferenceDetails: string;
  reason: string;
}

export interface PreferenceMemoryDetail {
  item: PreferenceMemoryItem;
  referenceStatus: "available" | "missing";
  reference: PreferenceMemoryReference | null;
}

const currentMemoryApi = DefaultApiFactory(
  new Configuration({ basePath: BASE_URL }),
  BASE_URL,
  axiosInstance,
);

const mapPreferenceItem = (
  item: CurrentMemoryPreferenceItem,
): PreferenceMemoryItem => ({
  name: item.name,
  summary: item.summary,
  createdAt: item.created_at,
  updatedAt: item.updated_at,
});

const mapPreferenceList = (
  data: CurrentMemoryPreferenceListData,
): PreferenceMemoryList => ({
  items: data.items.map(mapPreferenceItem),
  etag: data.etag,
  totalSize: data.total_size,
  updatedAt: data.updated_at,
});

const mapReference = (
  reference: CurrentMemoryReference,
): PreferenceMemoryReference => ({
  name: reference.name,
  summary: reference.summary,
  createdAt: reference.created_at,
  updatedAt: reference.updated_at,
  source: {
    kind: reference.source.kind,
    conversationId: reference.source.conversation_id,
  },
  applicationScenarios: reference.application_scenarios,
  preferenceDetails: reference.preference_details,
  reason: reference.reason,
});

export async function getSoulMemory(): Promise<
  CurrentMemorySnapshot<SoulDocument>
> {
  const { data } = (await currentMemoryApi.apiCoreMemorySoulGet()).data;
  return {
    document: data.document,
    updatedAt: data.updated_at,
  };
}

export async function patchSoulMemory(
  patch: SoulPatch,
): Promise<CurrentMemorySnapshot<SoulDocument>> {
  const { data } = (
    await currentMemoryApi.apiCoreMemorySoulPatch({
      currentMemorySoulPatchRequest: patch,
    })
  ).data;
  return {
    document: data.document,
    updatedAt: data.updated_at,
  };
}

export async function getProfileMemory(): Promise<
  CurrentMemorySnapshot<ProfileDocument>
> {
  const { data } = (await currentMemoryApi.apiCoreMemoryProfileGet()).data;
  return {
    document: data.document,
    updatedAt: data.updated_at,
  };
}

export async function patchProfileMemory(
  patch: ProfilePatch,
): Promise<CurrentMemorySnapshot<ProfileDocument>> {
  const { data } = (
    await currentMemoryApi.apiCoreMemoryProfilePatch({
      currentMemoryProfilePatchRequest: patch,
    })
  ).data;
  return {
    document: data.document,
    updatedAt: data.updated_at,
  };
}

export async function listPreferenceMemories(): Promise<PreferenceMemoryList> {
  const { data } = (
    await currentMemoryApi.apiCoreMemoryPreferencesGet()
  ).data;
  return mapPreferenceList(data);
}

export async function getPreferenceMemory(
  preferenceName: string,
): Promise<PreferenceMemoryDetail> {
  const { data } = (
    await currentMemoryApi.apiCoreMemoryPreferencesNameGet({
      name: preferenceName,
    })
  ).data;
  const reference =
    data.reference_status === "available" ? data.reference : null;
  if (data.reference_status === "available" && !reference) {
    throw new Error("Preference Reference is unavailable");
  }
  return {
    item: mapPreferenceItem(data.item),
    referenceStatus: data.reference_status,
    reference: reference ? mapReference(reference) : null,
  };
}

export async function reorderPreferenceMemories(
  orderedNames: string[],
  expectedEtag: string,
): Promise<PreferenceMemoryList> {
  const { data } = (
    await currentMemoryApi.apiCoreMemoryPreferencesOrderPut({
      currentMemoryPreferenceOrderRequest: {
        ordered_names: orderedNames,
        expected_etag: expectedEtag,
      },
    })
  ).data;
  return mapPreferenceList(data);
}

export async function deletePreferenceMemory(
  preferenceName: string,
): Promise<void> {
  await currentMemoryApi.apiCoreMemoryPreferencesNameDelete({
    name: preferenceName,
  });
}
