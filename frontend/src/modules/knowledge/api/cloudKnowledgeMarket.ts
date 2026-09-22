import { cloudResourceApi, silentCloudRequest } from "@/api/cloudClient";
import type { OfficialKnowledgeBase } from "../pages/list/knowledgeSquareData";

import type { CloudKnowledgeCatalogItem, CloudKnowledgeCatalogDetail } from "@/api/generated/core-client";
type CloudKnowledgeItem = CloudKnowledgeCatalogItem & Partial<Pick<CloudKnowledgeCatalogDetail, "sample_questions">>;

function toSquare(item: CloudKnowledgeItem): OfficialKnowledgeBase {
  if (!item || typeof item.catalog_key !== "string" || !item.catalog_key || !Number.isSafeInteger(item.version) || item.version < 1 || !["industry", "evaluation"].includes(item.category)) throw new Error("Invalid Cloud knowledge item");
  return {
    id: `cloud:${item.catalog_key}`, catalogKey: item.catalog_key, catalogSource: "cloud",
    type: item.category, name: item.name, desc: item.description, icon: item.icon, domain: item.domain,
    tags: Array.from(item.tags || []), source: item.data_source, updated: item.updated_at,
    questions: item.sample_questions || [], onlineAccessUrl: item.online_access_url || "",
    installed: false, latestVersion: String(item.version), installedVersion: "", updateAvailable: false,
    active: false, installState: "", datasetId: "",
  };
}

export async function listCloudKnowledgeMarket(signal?: AbortSignal): Promise<OfficialKnowledgeBase[]> {
  const items: OfficialKnowledgeBase[] = [];
  const cursors = new Set<string>();
  let cursor: string | undefined;
  do {
    signal?.throwIfAborted();
    const options = { ...silentCloudRequest, signal, timeout: 15000 };
    const response = await cloudResourceApi.apiCoreCloudKnowledgeMarketGet({ pageSize: 100, cursor }, options);
    const page = response.data.data;
    if (!page || !Array.isArray(page.items) || page.items.length > 100 || (page.next_cursor !== undefined && typeof page.next_cursor !== "string")) throw new Error("Invalid Cloud knowledge page");
    items.push(...page.items.map(toSquare));
    cursor = page.next_cursor;
    if (cursor) {
      if (cursor.length > 2048 || cursors.has(cursor)) throw new Error("Invalid Cloud knowledge cursor");
      cursors.add(cursor);
    }
  } while (cursor);
  signal?.throwIfAborted();
  return items;
}

export async function getCloudKnowledgeMarketDetail(key: string, signal?: AbortSignal): Promise<OfficialKnowledgeBase> {
  const options = { ...silentCloudRequest, signal, timeout: 15000 };
  const response = await cloudResourceApi.apiCoreCloudKnowledgeMarketItemsCatalogKeyGet({ catalogKey: key }, options);
  signal?.throwIfAborted();
  if (response.data.data?.catalog_key !== key) throw new Error("Invalid Cloud knowledge identity");
  return toSquare(response.data.data);
}
