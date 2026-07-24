import {
  Configuration,
  DefaultApiFactory,
  type EpisodeMemory,
  type EpisodeMemoryListData,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { EpisodeListItem } from "./episodeViewModel";

const coreConfig = new Configuration({ basePath: BASE_URL });
const episodeApi = DefaultApiFactory(coreConfig, BASE_URL, axiosInstance);

export interface EpisodeRecord extends EpisodeListItem {
  conversationId: string;
  episodeType: string;
  hitCount: number;
  occurredAtMs: number;
  sourceKind: string;
  summary: string;
}

export interface EpisodeListPage {
  items: EpisodeRecord[];
  nextPageToken: string;
  totalSize: number;
}

export interface ListEpisodesOptions {
  pageSize?: number;
  pageToken?: string;
}

const toStringValue = (value: string | number | undefined): string => {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number") {
    return String(value);
  }
  return "";
};

const toNumberValue = (value: number | string | undefined): number => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }
  return 0;
};

const normalizeEpisode = (
  value: EpisodeMemory | undefined,
): EpisodeRecord | null => {
  const id = toStringValue(value?.id).trim();
  if (!id) {
    return null;
  }

  return {
    id,
    conversationId: toStringValue(value?.conversation_id).trim(),
    episodeType: toStringValue(value?.episode_type).trim(),
    hitCount: toNumberValue(value?.hit_count),
    occurredAtMs: toNumberValue(value?.occurred_at_ms),
    recordedAtMs: toNumberValue(value?.recorded_at_ms),
    sourceKind: toStringValue(value?.source_kind).trim(),
    summary: toStringValue(value?.summary).trim(),
  };
};

const normalizeEpisodeList = (
  value: EpisodeMemoryListData | undefined,
): EpisodeListPage => {
  const items = Array.isArray(value?.items)
    ? value.items
        .map((item) => normalizeEpisode(item))
        .filter((item): item is EpisodeRecord => Boolean(item))
    : [];

  return {
    items,
    nextPageToken: toStringValue(value?.next_page_token).trim(),
    totalSize: toNumberValue(value?.total_size),
  };
};

export async function listEpisodes(
  options: ListEpisodesOptions = {},
): Promise<EpisodeListPage> {
  const response = await episodeApi.apiCoreMemoryEpisodesGet({
    pageSize: options.pageSize,
    pageToken: options.pageToken || undefined,
  });

  return normalizeEpisodeList(response.data.data);
}

export async function getEpisode(episodeId: string): Promise<EpisodeRecord> {
  const response = await episodeApi.apiCoreMemoryEpisodesEpisodeIdGet({
    episodeId,
  });
  const episode = normalizeEpisode(response.data.data);

  if (!episode) {
    throw new Error("Invalid episode detail response");
  }

  return episode;
}

export async function deleteEpisode(episodeId: string): Promise<void> {
  await episodeApi.apiCoreMemoryEpisodesEpisodeIdDelete({ episodeId });
}
