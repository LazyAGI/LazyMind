import { getProviderBrandSearchText, getProviderSearchText, type ProviderDisplayIdentity } from "./providerDisplayNames";
import { resolveSupplierService } from "./supplierServices";
import type { OrderedSupplier } from "./supplierOrdering";

export interface SupplierNavigationItem<T extends OrderedSupplier> extends OrderedSupplier {
  isBrandGroup: boolean;
  orderAliases: string[];
  children: T[];
}

/** Navigation parents own no endpoint, key or model state. Children keep their API UUID. */
export function projectSupplierNavigation<T extends OrderedSupplier>(providers: readonly T[]): SupplierNavigationItem<T>[] {
  const groups = new Map<string, T[]>();
  for (const provider of providers) {
    const service = resolveSupplierService(provider);
    const key = service ? `brand:${service.brandKey}` : provider.id;
    groups.set(key, [...(groups.get(key) || []), provider]);
  }
  return [...groups.entries()].map(([key, entries]) => {
    const children = [...entries].sort((a, b) =>
      (resolveSupplierService(a)?.order ?? 0) - (resolveSupplierService(b)?.order ?? 0) || a.id.localeCompare(b.id));
    const isBrandGroup = children.length > 1 && key.startsWith("brand:");
    const first = children[0];
    return {
      id: isBrandGroup ? key : first.id,
      name: isBrandGroup ? resolveSupplierService(first)!.brandKey : first.name,
      brandKey: first.brandKey || first.brand_key,
      serviceKey: first.serviceKey || first.service_key,
      isCustom: first.isCustom === true || first.is_custom === true,
      isBrandGroup,
      orderAliases: children.map((child) => child.id),
      children,
    };
  });
}

export function filterSupplierNavigation<T extends OrderedSupplier>(
  nodes: readonly SupplierNavigationItem<T>[],
  search: string,
  configuredOnly: boolean,
  configured: (provider: T) => boolean,
  language?: string,
): SupplierNavigationItem<T>[] {
  const keyword = search.trim().toLowerCase();
  return nodes.flatMap((node) => {
    const brandMatch = node.isBrandGroup && getProviderBrandSearchText(node, language).includes(keyword);
    const children = node.children.filter((child) =>
      (!configuredOnly || configured(child)) && (!keyword || brandMatch || getProviderSearchText(child, language).includes(keyword)));
    return children.length ? [{ ...node, children }] : [];
  });
}

export function hasProgrammingScopeNotice(provider: ProviderDisplayIdentity): boolean {
  return Boolean(programmingScopeLink(provider));
}

export function programmingScopeLink(provider: ProviderDisplayIdentity): string {
  const identity = resolveSupplierService(provider);
  if (!identity) return "";
  const key = `${identity.brandKey}:${identity.serviceKey}`;
  const links: Record<string, string> = {
    "Doubao:coding": "https://docs.volcengine.com/docs/ark/coding-plan-personal-get-started?lang=zh",
    "Doubao:agent": "https://docs.volcengine.com/docs/ark/agent-plan-personal-other-tools?lang=zh",
    "Qwen:coding": "https://help.aliyun.com/zh/model-studio/more-tools",
    "Qwen:token": "https://help.aliyun.com/zh/model-studio/more-tools",
    "Qwen:token_team": "https://help.aliyun.com/zh/model-studio/more-tools",
    "GLM:coding": "https://docs.bigmodel.cn/cn/coding-plan/tool/others",
    "Kimi:code": "https://www.kimi.com/code/docs/en/kimi-code/community-guidelines.html",
    "Minimax:m_plan": "https://platform.minimax.io/docs/m-plan/faq",
  };
  return links[key] || "";
}
