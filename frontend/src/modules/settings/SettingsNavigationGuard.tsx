import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Button, Form, Modal, theme } from "antd";
import type { FormInstance } from "antd";
import { UNSAFE_DataRouterContext, useBlocker, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";

interface SettingsDraft {
  dirty: boolean;
  saving?: boolean;
  save?: () => Promise<boolean>;
  discard?: () => void;
}

interface NavigationContext {
  register: (id: string, draft: SettingsDraft | null) => void;
  confirmLeave: (proceed: () => void, cancel?: () => void) => void;
  params: URLSearchParams;
  setEditor: (kind: string, item?: string, group?: string) => void;
}

const SettingsNavigationContext = createContext<NavigationContext | null>(null);

function RouteGuard({ shouldBlock, confirmLeave }: { shouldBlock: () => boolean; confirmLeave: NavigationContext["confirmLeave"] }) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => shouldBlock() && (
    currentLocation.pathname !== nextLocation.pathname || currentLocation.search !== nextLocation.search
  ));
  useEffect(() => {
    if (blocker.state === "blocked") confirmLeave(blocker.proceed, blocker.reset);
  }, [blocker, confirmLeave]);
  return null;
}

export function SettingsNavigationGuard({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const { token } = theme.useToken();
  const dataRouter = useContext(UNSAFE_DataRouterContext);
  const [params, setParams] = useSearchParams();
  const [drafts, setDrafts] = useState<Record<string, SettingsDraft>>({});
  const draftsRef = useRef(drafts);
  const [pending, setPending] = useState<{ proceed: () => void; cancel?: () => void } | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);
  const activeDrafts = Object.values(drafts).filter((draft) => draft.dirty || draft.saving);
  const busy = saving || activeDrafts.some((draft) => draft.saving);

  const register = useCallback((id: string, draft: SettingsDraft | null) => {
    const next = { ...draftsRef.current };
    if (draft) next[id] = draft;
    else delete next[id];
    draftsRef.current = next;
    setDrafts(next);
  }, []);
  const confirmLeave = useCallback((proceed: () => void, cancel?: () => void) => {
    if (!Object.values(draftsRef.current).some((draft) => draft.dirty || draft.saving)) {
      proceed();
      return;
    }
    setSaveFailed(false);
    setPending((current) => current || { proceed, cancel });
  }, []);
  const setEditor = useCallback((kind: string, item?: string, group?: string) => {
    const next = new URLSearchParams(params);
    for (const key of ["editor", "item", "group"]) next.delete(key);
    if (item) {
      next.set("editor", kind);
      next.set("item", item);
      if (group) next.set("group", group);
    }
    setParams(next);
  }, [params, setParams]);
  const context = useMemo(() => ({ register, confirmLeave, params, setEditor }), [register, confirmLeave, params, setEditor]);

  useEffect(() => {
    if (activeDrafts.length === 0) return;
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [activeDrafts.length]);

  const cancel = () => {
    pending?.cancel?.();
    setPending(null);
  };
  const discard = () => {
    activeDrafts.forEach((draft) => draft.discard?.());
    const proceed = pending?.proceed;
    draftsRef.current = {};
    setDrafts({});
    setPending(null);
    proceed?.();
  };
  const saveAndLeave = async () => {
    setSaving(true);
    setSaveFailed(false);
    try {
      for (const draft of activeDrafts) {
        if (draft.dirty && (!draft.save || !await draft.save())) {
          setSaveFailed(true);
          return;
        }
      }
      const proceed = pending?.proceed;
      setPending(null);
      proceed?.();
    } catch {
      setSaveFailed(true);
    } finally {
      setSaving(false);
    }
  };

  return <SettingsNavigationContext.Provider value={context}>
    {dataRouter ? <RouteGuard shouldBlock={() => Object.values(draftsRef.current).some((draft) => draft.dirty || draft.saving)} confirmLeave={confirmLeave} /> : null}
    {children}
    <Modal
      centered
      // Match Ant Design's confirmation layer, above editor portals and their popups.
      zIndex={token.zIndexPopupBase + 1000}
      open={Boolean(pending)}
      title={t("settingsPage.unsaved.title")}
      onCancel={busy ? undefined : cancel}
      closable={!busy}
      maskClosable={!busy}
      keyboard={!busy}
      footer={<>
        <Button disabled={busy} onClick={cancel}>{t("settingsPage.unsaved.stay")}</Button>
        <Button danger disabled={busy} onClick={discard}>{t("settingsPage.unsaved.discard")}</Button>
        {activeDrafts.length > 0 && activeDrafts.every((draft) => draft.save) ? (
          <Button type="primary" loading={saving} disabled={busy && !saving} onClick={() => void saveAndLeave()}>
            {t("settingsPage.unsaved.save")}
          </Button>
        ) : null}
      </>}
    >
      <p>{t(busy ? "settingsPage.unsaved.saving" : "settingsPage.unsaved.description")}</p>
      {saveFailed ? <p role="alert">{t("settingsPage.unsaved.saveFailed")}</p> : null}
    </Modal>
  </SettingsNavigationContext.Provider>;
}

// Optional outside Settings: reused configuration surfaces keep their existing navigation.
export function useSettingsDraft(draft: SettingsDraft) {
  const context = useContext(SettingsNavigationContext);
  const id = useId();
  const latest = useRef(draft);
  latest.current = draft;
  const register = context?.register;
  const hasSave = Boolean(draft.save);
  useEffect(() => {
    if (!register) return;
    register(id, {
      dirty: draft.dirty,
      saving: draft.saving,
      save: hasSave ? () => latest.current.save!() : undefined,
      discard: () => latest.current.discard?.(),
    });
    return () => register(id, null);
  }, [id, register, draft.dirty, draft.saving, hasSave]);
  const confirmClose = useCallback((proceed: () => void) => {
    if (context) context.confirmLeave(proceed);
    else proceed();
  }, [context]);
  const acceptSaved = useCallback(() => register?.(id, null), [register, id]);
  return Object.assign(confirmClose, { acceptSaved });
}

export function useSettingsFormDraft<T>(form: FormInstance<T>, active: boolean, saving = false) {
  Form.useWatch([], form);
  const [saved, setSaved] = useState<string | null>(null);
  const captureSavedValues = useCallback(() => setSaved(JSON.stringify(form.getFieldsValue(true))), [form]);
  const confirmClose = useSettingsDraft({
    dirty: active && saved !== null && JSON.stringify(form.getFieldsValue(true)) !== saved,
    saving: active && saving,
    discard: () => setSaved(null),
  });
  return { captureSavedValues, confirmClose, acceptSaved: confirmClose.acceptSaved };
}

// Only non-sensitive resource identifiers belong in the URL. Values stay in the form.
export function useSettingsEditor(kind: string) {
  const context = useContext(SettingsNavigationContext);
  const select = useCallback((item?: string, group?: string) => context?.setEditor(kind, item, group), [context?.setEditor, kind]);
  return {
    managed: Boolean(context),
    item: context?.params.get("editor") === kind ? context.params.get("item") : null,
    group: context?.params.get("editor") === kind ? context.params.get("group") : null,
    select,
  };
}
