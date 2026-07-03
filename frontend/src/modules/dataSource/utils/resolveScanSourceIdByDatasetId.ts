import { dataSourceScanApi } from "../api/clients";
import { getScanSourceId } from "./scanAccessors";

const PAGE_SIZE = 200;
const MAX_PAGES = 20;

export async function resolveScanSourceIdByDatasetId(
  datasetId: string,
): Promise<string | null> {
  const normalizedDatasetId = `${datasetId || ""}`.trim();
  if (!normalizedDatasetId) {
    return null;
  }

  for (let page = 1; page <= MAX_PAGES; page += 1) {
    const response = await dataSourceScanApi.listSources({
      page,
      pageSize: PAGE_SIZE,
    });
    const items = response.data.items || [];
    const matched = items.find(
      (item) => `${item.dataset_id || ""}`.trim() === normalizedDatasetId,
    );
    if (matched) {
      return getScanSourceId(matched);
    }

    const total = Number(response.data.total || 0);
    if (page * PAGE_SIZE >= total || items.length === 0) {
      break;
    }
  }

  return null;
}
