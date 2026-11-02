import { getProviderBrandDisplayName, type ProviderDisplayIdentity } from "./providerDisplayNames";

export interface OrderedSupplier extends ProviderDisplayIdentity { id: string; orderAliases?: readonly string[]; isBrandGroup?: boolean }

export function orderSuppliers<T extends OrderedSupplier>(
  providers: readonly T[], savedOrder: readonly string[] = [],
): T[] {
  const rank = new Map(savedOrder.map((id, index) => [id, index]));
  const positionOf = (provider: T) => Math.min(...[provider.id, ...(provider.orderAliases || [])].map((id) => rank.get(id) ?? Infinity));
  return [...providers].sort((left, right) => {
    const position = positionOf(left) - positionOf(right);
    if (position && !Number.isNaN(position)) return position;
    const initialPriority = (item: T) => !(item.isCustom || item.is_custom) && getProviderBrandDisplayName(item, "en-US") === "SenseNova" ? 0 : 1;
    const priority = initialPriority(left) - initialPriority(right);
    if (priority) return priority;
    return getProviderBrandDisplayName(left, "en-US").localeCompare(
      getProviderBrandDisplayName(right, "en-US"), "en", { sensitivity: "base", numeric: true },
    ) || left.id.localeCompare(right.id);
  });
}

/** Filtered-out suppliers retain their positions when visible rows are moved. */
export function reorderSuppliers<T extends OrderedSupplier>(
  providers: T[], visibleIds: readonly string[], movedId: string, targetId: string,
): T[] {
  const moved = providers.find((item) => item.id === movedId);
  const target = providers.find((item) => item.id === targetId);
  if (!moved || !target || movedId === targetId) return providers;
  const visible = new Set(visibleIds);
  const movable = providers.filter((item) => visible.has(item.id));
  const from = movable.findIndex((item) => item.id === movedId);
  const to = movable.findIndex((item) => item.id === targetId);
  if (from < 0 || to < 0) return providers;
  movable.splice(to, 0, movable.splice(from, 1)[0]);
  const reorderedIds = new Set(movable.map((item) => item.id));
  let index = 0;
  return providers.map((item) => reorderedIds.has(item.id) ? movable[index++] : item);
}
