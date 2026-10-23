import { useEffect, useMemo, useState } from "react";
import type { UserInfo } from "@/components/auth";
import { orderSuppliers, reorderSuppliers, type OrderedSupplier } from "../supplierOrdering";

const storagePrefix = "lazymind:supplier-order:";
const userStorageKey = "lazymind:user";
const userChangeEvent = "lazymind:user-change";

function storageKey(): string {
  try {
    const user = JSON.parse(localStorage.getItem(userStorageKey) || "null") as UserInfo | null;
    const id = user?.userId || user?.username;
    if (!id) return "";
    return storagePrefix + JSON.stringify([
      id, user?.tenantId || user?.tenant_id || user?.tenantKey || user?.tenant_key || "",
    ]);
  } catch { return ""; }
}

function readOrder(key: string): string[] {
  if (!key) return [];
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key) || "[]");
    return Array.isArray(value)
      ? [...new Set(value.filter((id): id is string => typeof id === "string" && id.length > 0 && id.length <= 255))].slice(0, 1000)
      : [];
  } catch { return []; }
}

export function useSupplierOrder<T extends OrderedSupplier>(suppliers: readonly T[]) {
  const scope = storageKey();
  const [saved, setSaved] = useState(() => ({ scope, ids: readOrder(scope) }));
  const providers = useMemo(() => orderSuppliers(suppliers,
    saved.scope === scope ? saved.ids : readOrder(scope)), [suppliers, scope, saved]);

  useEffect(() => {
    const refresh = () => {
      const nextScope = storageKey();
      setSaved({ scope: nextScope, ids: readOrder(nextScope) });
    };
    const changed = (event: StorageEvent) => {
      if (!event.key || event.key === userStorageKey || event.key.startsWith(storagePrefix)) refresh();
    };
    window.addEventListener(userChangeEvent, refresh);
    window.addEventListener("storage", changed);
    return () => {
      window.removeEventListener(userChangeEvent, refresh);
      window.removeEventListener("storage", changed);
    };
  }, []);

  const reorder = (visibleIds: readonly string[], movedId: string, targetId: string) => {
    // A drag started under another account must not write this user's order.
    if (storageKey() !== scope) return;
    setSaved((current) => {
      const ordered = orderSuppliers(suppliers, current.scope === scope ? current.ids : readOrder(scope));
      const next = reorderSuppliers(ordered, visibleIds, movedId, targetId);
      if (next === ordered) return current;
      const ids = next.map((item) => item.id);
      if (scope) {
        try { localStorage.setItem(scope, JSON.stringify(ids)); } catch { /* Keep the order for this page when storage is unavailable. */ }
      }
      return { scope, ids };
    });
  };
  return { providers, reorder };
}
