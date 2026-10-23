import { getProviderDisplayName, type ProviderDisplayIdentity } from "./providerDisplayNames";

export interface OrderedSupplier extends ProviderDisplayIdentity { id: string }

/** Custom identities stay first; SenseNova is the only pinned built-in brand. */
export function supplierOrderGroup(provider: OrderedSupplier): number {
  if (provider.isCustom || provider.is_custom) return 0;
  return getProviderDisplayName(provider, "en-US") === "SenseNova" ? 1 : 2;
}

export function orderSuppliers<T extends OrderedSupplier>(
  providers: readonly T[], savedOrder: readonly string[] = [],
): T[] {
  const rank = new Map(savedOrder.map((id, index) => [id, index]));
  return [...providers].sort((left, right) => {
    const section = supplierOrderGroup(left) - supplierOrderGroup(right);
    if (section) return section;
    const position = (rank.get(left.id) ?? Infinity) - (rank.get(right.id) ?? Infinity);
    if (position && !Number.isNaN(position)) return position;
    return getProviderDisplayName(left, "en-US").localeCompare(
      getProviderDisplayName(right, "en-US"), "en", { sensitivity: "base", numeric: true },
    ) || left.id.localeCompare(right.id);
  });
}

/** Filtered-out suppliers retain their positions when visible rows are moved. */
export function reorderSuppliers<T extends OrderedSupplier>(
  providers: T[], visibleIds: readonly string[], movedId: string, targetId: string,
): T[] {
  const moved = providers.find((item) => item.id === movedId);
  const target = providers.find((item) => item.id === targetId);
  if (!moved || !target || movedId === targetId || supplierOrderGroup(moved) === 1 ||
      supplierOrderGroup(moved) !== supplierOrderGroup(target)) return providers;
  const visible = new Set(visibleIds);
  const movable = providers.filter((item) => visible.has(item.id) && supplierOrderGroup(item) === supplierOrderGroup(moved));
  const from = movable.findIndex((item) => item.id === movedId);
  const to = movable.findIndex((item) => item.id === targetId);
  if (from < 0 || to < 0) return providers;
  movable.splice(to, 0, movable.splice(from, 1)[0]);
  const reorderedIds = new Set(movable.map((item) => item.id));
  let index = 0;
  return providers.map((item) => reorderedIds.has(item.id) ? movable[index++] : item);
}
