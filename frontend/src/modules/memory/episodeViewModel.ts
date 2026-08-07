export interface EpisodeListItem {
  id: string;
  recordedAtMs: number;
}

export interface EpisodeDateGroup<T extends EpisodeListItem> {
  dateKey: string;
  items: T[];
}

export const sortEpisodesByRecordedTime = <T extends EpisodeListItem>(
  episodes: T[],
): T[] =>
  [...episodes].sort(
    (left, right) =>
      right.recordedAtMs - left.recordedAtMs ||
      right.id.localeCompare(left.id),
  );

const createDateKeyFormatter = (timeZone?: string) =>
  new Intl.DateTimeFormat("en-US", {
    day: "2-digit",
    month: "2-digit",
    timeZone,
    year: "numeric",
  });

const formatDateKey = (
  timestampMs: number,
  formatter: Intl.DateTimeFormat,
) => {
  const parts = formatter.formatToParts(new Date(timestampMs));
  const getPart = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((part) => part.type === type)?.value || "";

  return `${getPart("year")}-${getPart("month")}-${getPart("day")}`;
};

export const groupEpisodesByRecordedDate = <
  T extends EpisodeListItem,
>(
  episodes: T[],
  timeZone?: string,
): EpisodeDateGroup<T>[] => {
  const formatter = createDateKeyFormatter(timeZone);
  const groups = new Map<string, T[]>();

  sortEpisodesByRecordedTime(episodes).forEach((episode) => {
    const dateKey = formatDateKey(episode.recordedAtMs, formatter);
    const items = groups.get(dateKey);

    if (items) {
      items.push(episode);
    } else {
      groups.set(dateKey, [episode]);
    }
  });

  return Array.from(groups, ([dateKey, items]) => ({ dateKey, items }));
};

export const mergeEpisodePages = <T extends EpisodeListItem>(
  current: T[],
  incoming: T[],
): T[] => {
  const merged = new Map(current.map((episode) => [episode.id, episode]));

  incoming.forEach((episode) => {
    if (!merged.has(episode.id)) {
      merged.set(episode.id, episode);
    }
  });

  return Array.from(merged.values());
};
