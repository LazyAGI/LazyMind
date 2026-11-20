import { listEpisodes, type EpisodeListPage } from "./episodeApi";

export function createEpisodePager(pageSize: number) {
  const cursors = new Map<number, string>();
  let cursorOnly = false;

  function remember(page: number, result: EpisodeListPage) {
    if (page === 1) cursors.clear();
    if (result.nextPageToken) cursors.set(page + 1, result.nextPageToken);
    else cursors.delete(page + 1);
    return result;
  }

  return {
    clearCursors: () => cursors.clear(),
    async load(page: number): Promise<EpisodeListPage> {
      if (!cursorOnly || page === 1) {
        const result = await listEpisodes({ pageSize, page });
        // Older Core versions ignore the page parameter and return page one's cursor again.
        if (page === 1 || !result.nextPageToken || result.nextPageToken !== cursors.get(2)) {
          return remember(page, result);
        }
        cursorOnly = true;
      }

      let current = 1;
      for (const known of cursors.keys()) {
        if (known <= page && known > current) current = known;
      }
      while (true) {
        const result = remember(current, await listEpisodes({ pageSize, pageToken: cursors.get(current) }));
        if (current === page) return result;
        if (!result.nextPageToken) return { ...result, items: [] };
        current += 1;
      }
    },
  };
}
