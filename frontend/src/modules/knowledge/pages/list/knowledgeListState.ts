import { ALL_TAGS } from "../../constants/common";
import type { KnowledgeMineCloudSource, KnowledgeMineSort } from "./knowledgeMineFilters";
import type { KnowledgeSquareInstallStatus, KnowledgeSquareType } from "./knowledgeSquareData";

export type SourceCategory = "local" | "cloudArchive" | "official";

export interface KnowledgeListState {
  view: "mine" | "square";
  source: SourceCategory;
  scene: string;
  keyword: string;
  tag: string;
  officialTag: string;
  sort: KnowledgeMineSort;
  cloudSource: KnowledgeMineCloudSource;
  page: number;
  pageSize: number;
  squareType: KnowledgeSquareType;
  squareDomain: string;
  squareStatus: KnowledgeSquareInstallStatus;
  squareKeyword: string;
}

const defaults: KnowledgeListState = {
  view: "mine", source: "local", scene: "", keyword: "", tag: ALL_TAGS,
  officialTag: ALL_TAGS, sort: "all", cloudSource: "all", page: 1,
  pageSize: 10, squareType: "industry", squareDomain: "",
  squareStatus: "all", squareKeyword: "",
};

function choice<T extends string>(value: string | null, choices: readonly T[], fallback: T): T {
  return choices.includes(value as T) ? value as T : fallback;
}

function positiveInteger(value: string | null, fallback: number, max: number) {
  const number = Number(value);
  return Number.isSafeInteger(number) && number > 0 && number <= max ? number : fallback;
}

export function readKnowledgeListState(search: string): KnowledgeListState {
  const params = new URLSearchParams(search);
  return {
    view: choice(params.get("view"), ["mine", "square"], defaults.view),
    source: choice(params.get("source"), ["local", "cloudArchive", "official"], defaults.source),
    scene: params.get("scene")?.trim() || "",
    keyword: params.get("keyword") || "",
    tag: params.get("tag") || ALL_TAGS,
    officialTag: params.get("officialTag") || ALL_TAGS,
    sort: choice(params.get("sort"), ["all", "recent_used", "most_used", "latest_updated"], defaults.sort),
    cloudSource: choice(params.get("cloudSource"), ["all", "local", "feishu", "notion"], defaults.cloudSource),
    page: positiveInteger(params.get("page"), defaults.page, Number.MAX_SAFE_INTEGER),
    pageSize: positiveInteger(params.get("pageSize"), defaults.pageSize, 100),
    squareType: choice(params.get("squareType"), ["industry", "evaluation"], defaults.squareType),
    squareDomain: params.get("squareDomain") || "",
    squareStatus: choice(params.get("squareStatus"), ["all", "installed", "uninstalled", "updatable"], defaults.squareStatus),
    squareKeyword: params.get("squareKeyword") || "",
  };
}

// Only normalize our own parameters; settings/cloud-creation entry parameters survive.
export function writeKnowledgeListState(search: string, patch: Partial<KnowledgeListState>) {
  const params = new URLSearchParams(search);
  const state = { ...readKnowledgeListState(search), ...patch };
  for (const key of Object.keys(defaults) as (keyof KnowledgeListState)[]) {
    if (state[key] === defaults[key]) params.delete(key);
    else params.set(key, String(state[key]));
  }
  const result = params.toString();
  return result ? `?${result}` : "";
}

export function getKnowledgeListPage(page: number, pageSize: number, total: number) {
  return Math.min(page, Math.max(1, Math.ceil(total / pageSize)));
}
