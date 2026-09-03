import {
  Configuration,
  type PreferenceOrganizerTaskData,
  DefaultApiFactory,
  type CurrentMemoryOperation,
  type CurrentMemoryOperationsRequest,
  type CurrentMemoryPreferenceItem,
  type CurrentMemoryPreferenceListData,
  type CurrentMemoryPresentation,
  type CurrentMemoryReference,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";

export type MemoryValue = string | null | string[] | MemoryDocument;
export interface MemoryDocument { [key: string]: MemoryValue }
export type SoulDocument = MemoryDocument;
export type MemoryOperation = CurrentMemoryOperation;
export type MemoryPatch = CurrentMemoryOperationsRequest;
export type SoulPatch = MemoryPatch;
export type ProfileDocument = MemoryDocument;
export type ProfilePatch = MemoryPatch;

export interface CurrentMemorySnapshot<TDocument> {
  document: TDocument;
  templateVersion: number;
  presentation: CurrentMemoryPresentation;
  updatedAt: number;
}

export interface PreferenceMemoryItem {
  name: string;
  summary: string;
  createdAt: string;
  updatedAt: string;
}

export interface PreferenceCharacterBudget {
  usedChars: number;
  maxChars: number;
}

export interface PreferenceMemoryList {
  items: PreferenceMemoryItem[];
  etag: string;
  totalSize: number;
  updatedAt: number;
  budget?: PreferenceCharacterBudget;
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
): PreferenceMemoryList => {
  const projection = data.projection_state;
  return {
    items: data.items.map(mapPreferenceItem),
    etag: data.etag,
    totalSize: data.total_size,
    updatedAt: data.updated_at,
    ...(projection
      ? {
          budget: {
            usedChars: projection.full_projection_chars,
            maxChars: projection.max_chars,
          },
        }
      : {}),
  };
};

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
    document: data.document as SoulDocument,
    templateVersion: data.template_version,
    presentation: data.presentation,
    updatedAt: data.updated_at,
  };
}

export async function patchSoulMemory(
  patch: SoulPatch,
): Promise<CurrentMemorySnapshot<SoulDocument>> {
  const { data } = (
    await currentMemoryApi.apiCoreMemorySoulPatch({
      currentMemoryOperationsRequest: patch,
    })
  ).data;
  return {
    document: data.document as SoulDocument,
    templateVersion: data.template_version,
    presentation: data.presentation,
    updatedAt: data.updated_at,
  };
}

export async function getProfileMemory(): Promise<
  CurrentMemorySnapshot<ProfileDocument>
> {
  const { data } = (await currentMemoryApi.apiCoreMemoryProfileGet()).data;
  return {
    document: data.document as ProfileDocument,
    templateVersion: data.template_version,
    presentation: data.presentation,
    updatedAt: data.updated_at,
  };
}

export async function patchProfileMemory(
  patch: ProfilePatch,
): Promise<CurrentMemorySnapshot<ProfileDocument>> {
  const { data } = (
    await currentMemoryApi.apiCoreMemoryProfilePatch({
      currentMemoryOperationsRequest: patch,
    })
  ).data;
  return {
    document: data.document as ProfileDocument,
    templateVersion: data.template_version,
    presentation: data.presentation,
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

export type PreferenceOrganizerTask = PreferenceOrganizerTaskData;

export async function getLatestPreferenceOrganizer(signal?: AbortSignal): Promise<PreferenceOrganizerTask | null> {
  const response = await currentMemoryApi.apiCoreMemoryPreferencesOrganizeGet({ signal });
  return response.data.data ?? null;
}

export async function submitPreferenceOrganizer(): Promise<PreferenceOrganizerTask> {
  const response = await currentMemoryApi.apiCoreMemoryPreferencesOrganizePost();
  if (!response.data.data) throw new Error("Missing organizer task response");
  return response.data.data;
}

export function isPreferenceOrganizing(error: unknown): boolean {
  const response = (error as { response?: { data?: { code?: number; data?: { error_code?: string } } } })?.response?.data;
  return response?.code === 2002321 || response?.data?.error_code === "preference_organizing";
}
