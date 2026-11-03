import { useEffect, useMemo, useSyncExternalStore } from "react";
import { AgentAppsAuth } from "@/components/auth";
import * as api from "./api";

type Snapshot = { inspections: api.InspectedSource[]; items: api.ImportItem[]; inspectError: boolean; queryError: boolean };
const entries = new Map<string, SourceState>();

class SourceState {
  snapshot: Snapshot = { inspections: [], items: [], inspectError: false, queryError: false };
  listeners = new Set<() => void>();
  inspection?: Promise<void>;
  inspectionVersion?: string;
  query?: Promise<void>;
  pollers = new Set<symbol>();
  timer?: ReturnType<typeof setTimeout>;
  constructor(readonly context: api.SourceContext, readonly key: string) {}
  emit(patch: Partial<Snapshot>) {
    this.snapshot = { ...this.snapshot, ...patch };
    this.listeners.forEach(listener => listener());
  }
  subscribe = (listener: () => void) => {
    entries.set(this.key, this);
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
      if (!this.listeners.size) entries.delete(this.key);
    };
  };
  getSnapshot = () => this.snapshot;
  inspect = (version: string): Promise<void> => {
    if (this.inspection) return this.inspection;
    if (this.inspectionVersion === version && !this.snapshot.inspectError) return Promise.resolve();
    this.inspection = api.inspectSources(this.context).then(inspections => {
      this.inspectionVersion = version;
      this.emit({ inspections, inspectError: false });
    }).catch(() => this.emit({ inspectError: true })).finally(() => { this.inspection = undefined; });
    return this.inspection;
  };
  refresh = (): Promise<void> => {
    if (this.query) return this.query;
    this.query = api.listSourceImports(this.context).then(rows => {
      const previous = this.snapshot.items;
      this.emit({ items: rows.map(row => row.status === "unknown"
        ? { ...row, ...previous.find(old => old.item_id === row.item_id), state_unavailable: true } : row), queryError: false });
    }).catch(() => this.emit({ queryError: true })).finally(() => {
      this.query = undefined;
      this.schedule();
    });
    return this.query;
  };
  schedule = () => {
    clearTimeout(this.timer);
    if (this.pollers.size && !document.hidden && this.snapshot.items.some(row =>
      row.state_unavailable || ["pending", "running", "unknown"].includes(row.status))) {
      this.timer = setTimeout(() => void this.refresh(), 3000);
    }
  };
  onVisibility = () => {
    clearTimeout(this.timer);
    if (!document.hidden && this.pollers.size) void this.refresh();
  };
  poll = () => {
    const id = Symbol();
    if (!this.pollers.size) {
      document.addEventListener("visibilitychange", this.onVisibility);
      if (!document.hidden) void this.refresh();
    }
    this.pollers.add(id);
    return () => {
      this.pollers.delete(id);
      if (!this.pollers.size) {
        clearTimeout(this.timer);
        document.removeEventListener("visibilitychange", this.onVisibility);
      }
    };
  };
}

export function sourceContextKey(context: api.SourceContext) {
  const owner = AgentAppsAuth.getUserInfo()?.userId ?? "";
  return JSON.stringify([owner, Object.entries(context).sort(([a], [b]) => a.localeCompare(b))]);
}

export function useSourceImportState(context: api.SourceContext, opened: boolean) {
  const key = sourceContextKey(context);
  const entry = useMemo(() => {
    const current = entries.get(key) ?? new SourceState(context, key);
    entries.set(key, current);
    return current;
  }, [key]);
  const snapshot = useSyncExternalStore(entry.subscribe, entry.getSnapshot);
  useEffect(() => opened ? entry.poll() : undefined, [entry, opened]);
  return { ...snapshot, refresh: entry.refresh, inspect: entry.inspect };
}
