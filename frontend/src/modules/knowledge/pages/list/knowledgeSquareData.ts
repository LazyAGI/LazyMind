import type {
  KnowledgeMarketDetailOpenAPIResponse,
  KnowledgeMarketInstallsOpenAPIResponseItem,
  KnowledgeMarketListItemOpenAPIResponse,
} from "@/api/generated/core-client";

export type KnowledgeSquareType = "industry" | "evaluation";
export type KnowledgeSquareInstallStatus = "all" | "installed" | "uninstalled";

export interface OfficialKnowledgeBase {
  id: string;
  type: KnowledgeSquareType;
  domain: string;
  icon: string;
  name: string;
  desc: string;
  tags: string[];
  updated: string;
  source: string;
  questions: string[];
  onlineAccessUrl: string;
  installed: boolean;
  active: boolean;
  installState: string;
  datasetId: string;
  installedAt?: string;
}

function toSquareType(category: string): KnowledgeSquareType {
  return category === "evaluation" ? "evaluation" : "industry";
}

export function mergeKnowledgeMarketItems(
  catalogItems: KnowledgeMarketListItemOpenAPIResponse[],
  installs: KnowledgeMarketInstallsOpenAPIResponseItem[],
): OfficialKnowledgeBase[] {
  const installsByItem = new Map(
    installs.map((install) => [install.market_item_id, install]),
  );

  return catalogItems.map((item) => {
    const install = installsByItem.get(item.id);
    return {
      id: item.id,
      type: toSquareType(item.category),
      domain: item.domain,
      icon: item.icon,
      name: item.name,
      desc: item.description,
      tags: item.tags || [],
      updated: item.updated_at,
      source: item.data_source,
      questions: [],
      onlineAccessUrl: item.online_access_url,
      installed: Boolean(install?.dataset_id),
      active: Boolean(install?.active),
      installState: install?.install_state || "",
      datasetId: install?.dataset_id || "",
      installedAt: install?.installed_at,
    };
  });
}

export function mergeKnowledgeMarketDetail(
  item: OfficialKnowledgeBase,
  detail: KnowledgeMarketDetailOpenAPIResponse & {
    online_access_url?: string;
  },
): OfficialKnowledgeBase {
  return {
    ...item,
    domain: detail.domain,
    icon: detail.icon,
    name: detail.name,
    desc: detail.description,
    tags: detail.tags || [],
    updated: detail.updated_at,
    source: detail.data_source,
    questions: detail.sample_questions || [],
    onlineAccessUrl: detail.online_access_url || item.onlineAccessUrl,
  };
}

export function filterOfficialKnowledgeBases({
  items,
  type,
  domain,
  status,
  keyword,
}: {
  items: OfficialKnowledgeBase[];
  type: KnowledgeSquareType;
  domain: string;
  status: KnowledgeSquareInstallStatus;
  keyword: string;
}) {
  const normalizedKeyword = keyword.trim().toLocaleLowerCase();

  return items.filter((item) => {
    const matchesStatus =
      status === "all" ||
      (status === "installed" && item.installed) ||
      (status === "uninstalled" && !item.installed);
    const haystack = [item.name, item.desc, item.domain, ...item.tags]
      .join(" ")
      .toLocaleLowerCase();

    return (
      item.type === type &&
      (domain === "" || item.domain === domain) &&
      matchesStatus &&
      (!normalizedKeyword || haystack.includes(normalizedKeyword))
    );
  });
}
