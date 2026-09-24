import { vi } from "vitest";
import messages from "@/i18n/locales/zh-CN";

export function desktopTestTranslation(key: string, values: Record<string, unknown> = {}) {
  const value = key.split(".").reduce<unknown>((current, part) =>
    current && typeof current === "object" ? (current as Record<string, unknown>)[part] : undefined, messages);
  return typeof value === "string" ? value.replace(/{{(\w+)}}/g, (_, name) => String(values[name] ?? "")) : key;
}

export function installDesktopTestDOM() {
  window.matchMedia = vi.fn().mockImplementation((media: string) => ({
    media, matches: false, onchange: null, addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  }));
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
}

export function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

export function localSkill(id: string, name = id) {
  return { id, name, description: `${name} local description`, category: "internal", tags: [],
    content: "", headRevisionId: `revision-${id}`, autoEvo: false, isEnabled: false };
}

export function cloudResource(id: string, kind: "skill" | "workflow" = "skill", overrides: Record<string, unknown> = {}) {
  return { resource_id: id, resource_type: kind, resource_name: id, content_size: 20,
    format_schema: "lazymind.resource-manifest/v2", updated_at: "2026-09-07T00:00:00Z",
    presence_status: "download_required", local_exists: false, ...overrides };
}
