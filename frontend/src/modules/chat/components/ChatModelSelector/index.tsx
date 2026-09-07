import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  CheckOutlined,
  DownOutlined,
  ReloadOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Popover, Spin, Tooltip, message } from "antd";
import { useTranslation } from "react-i18next";
import { CHAT_OPEN_MODEL_SELECTOR_EVENT } from "@/modules/chat/constants/chat";
import {
  chatModelSelectionKey,
  toChatModelSelectionRequest,
  useModelSelectionStore,
  type ChatModelCatalog,
  type ChatModelOption,
  type ChatModelProvider,
  type ChatModelSelection,
  type ChatModelSelectionRequest,
} from "@/modules/chat/store/modelSelection";
import {
  fetchChatModelCatalog,
  updateConversationChatModel,
} from "./api";
import "./index.scss";

type LoadStatus = "loading" | "ready" | "error";

interface ChatModelSelectorProps {
  conversationId?: string;
  disabled?: boolean;
  disabledReason?: string;
  onSavingChange?: (saving: boolean) => void;
  onSelectionChange?: (
    request: ChatModelSelectionRequest,
    selection: ChatModelSelection,
  ) => void;
}

function isRealConversationId(conversationId?: string): conversationId is string {
  return Boolean(conversationId && !conversationId.startsWith("temp_"));
}

function modelProviderForSelection(
  providers: ChatModelProvider[],
  selection?: ChatModelSelection,
): ChatModelProvider | undefined {
  return providers.find((provider) =>
    provider.models.some((model) => model.id === selection?.model_id),
  );
}

function providerKey(provider: ChatModelProvider): string {
  return `${provider.id}:${provider.source ?? "own"}`;
}

function isModelAvailable(model: ChatModelOption): boolean {
  return model.available !== false && model.availability !== "unavailable";
}

function resolveStoredSelection(
  selection: ChatModelSelection,
  providers: ChatModelProvider[],
  autoAvailable: boolean,
): ChatModelSelection {
  if (selection.mode === "auto") {
    return {
      ...selection,
      availability: autoAvailable ? "available" : "unavailable",
    };
  }
  const provider = modelProviderForSelection(providers, selection);
  const model = provider?.models.find((item) => item.id === selection.model_id);
  if (!provider || !model) {
    return { ...selection, availability: "unavailable" };
  }
  return {
    ...selection,
    provider_name: provider.name,
    model_name: model.name,
    group_name: model.group_name,
    source: model.source ?? provider.source ?? selection.source,
    availability: isModelAvailable(model)
      ? (model.availability ?? "available")
      : "unavailable",
  };
}

function modelLabel(
  provider: ChatModelProvider,
  model: ChatModelOption,
): string {
  return `${provider.name} · ${model.name}`;
}

const ChatModelSelector = ({
  conversationId,
  disabled = false,
  disabledReason,
  onSavingChange,
  onSelectionChange,
}: ChatModelSelectorProps) => {
  const { t } = useTranslation();
  const normalizedConversationId = isRealConversationId(conversationId)
    ? conversationId
    : undefined;
  const selectionKey = chatModelSelectionKey(normalizedConversationId);
  const setStoredSelection = useModelSelectionStore((state) => state.setSelection);
  const [catalog, setCatalog] = useState<ChatModelCatalog | null>(null);
  const [loadStatus, setLoadStatus] = useState<LoadStatus>("loading");
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [switchError, setSwitchError] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [announcement, setAnnouncement] = useState("");
  const disabledReasonId = useId();
  const loadSequenceRef = useRef(0);
  const saveSequenceRef = useRef(0);
  const savingRef = useRef(false);
  const loadControllerRef = useRef<AbortController | null>(null);
  const saveControllerRef = useRef<AbortController | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const previousDisabledRef = useRef(disabled);
  const translateRef = useRef(t);
  const onSavingChangeRef = useRef(onSavingChange);
  const onSelectionChangeRef = useRef(onSelectionChange);
  translateRef.current = t;
  onSavingChangeRef.current = onSavingChange;
  onSelectionChangeRef.current = onSelectionChange;

  const commitSelection = useCallback(
    (selection: ChatModelSelection) => {
      setStoredSelection(selectionKey, selection);
      const request = toChatModelSelectionRequest(selection);
      if (request) onSelectionChangeRef.current?.(request, selection);
    },
    [selectionKey, setStoredSelection],
  );

  const loadCatalog = useCallback(async () => {
    const sequence = ++loadSequenceRef.current;
    loadControllerRef.current?.abort();
    const controller = new AbortController();
    loadControllerRef.current = controller;
    setLoadStatus("loading");
    setSwitchError("");
    try {
      const nextCatalog = await fetchChatModelCatalog(
        normalizedConversationId,
        controller.signal,
      );
      if (controller.signal.aborted || sequence !== loadSequenceRef.current) {
        return;
      }
      const providers = Array.isArray(nextCatalog.providers)
        ? nextCatalog.providers.map((provider) => ({
            ...provider,
            models: Array.isArray(provider.models) ? provider.models : [],
          }))
        : [];
      const storedNewChatSelection = !normalizedConversationId
        ? useModelSelectionStore.getState().selections[selectionKey]
        : undefined;
      const preservedSelection = storedNewChatSelection
        ? resolveStoredSelection(
            storedNewChatSelection,
            providers,
            nextCatalog.auto_available,
          )
        : undefined;
      const normalizedCatalog = {
        ...nextCatalog,
        providers,
        selection: preservedSelection
          ? { ...preservedSelection, version: nextCatalog.selection.version }
          : nextCatalog.selection,
      };
      setCatalog(normalizedCatalog);
      setLoadStatus("ready");
      commitSelection(normalizedCatalog.selection);
    } catch {
      if (controller.signal.aborted || sequence !== loadSequenceRef.current) {
        return;
      }
      setCatalog(null);
      setLoadStatus("error");
      setAnnouncement(translateRef.current("chat.modelSelectorLoadFailed"));
    }
  }, [commitSelection, normalizedConversationId, selectionKey]);

  useEffect(() => {
    void loadCatalog();
    return () => {
      loadSequenceRef.current += 1;
      loadControllerRef.current?.abort();
      saveSequenceRef.current += 1;
      saveControllerRef.current?.abort();
      saveControllerRef.current = null;
      if (savingRef.current) {
        savingRef.current = false;
        onSavingChangeRef.current?.(false);
      }
    };
  }, [loadCatalog]);

  useEffect(() => {
    const wasDisabled = previousDisabledRef.current;
    previousDisabledRef.current = disabled;
    if (wasDisabled && !disabled) void loadCatalog();
  }, [disabled, loadCatalog]);

  const catalogBlockedReason = (() => {
    switch (catalog?.switch_blocked_reason) {
      case "generating":
        return t("chat.modelSelectorGenerating");
      case "workflow_running":
        return t("chat.modelSelectorWorkflowRunning");
      case "background_task_running":
        return t("chat.modelSelectorBackgroundTaskRunning");
      default:
        return t("chat.modelSelectorBusy");
    }
  })();
  const switchBlockedReason = saving
    ? t("chat.modelSelectorSwitching")
    : loadStatus === "loading"
      ? t("chat.modelSelectorLoading")
      : (disabled ? disabledReason : undefined) || catalogBlockedReason;
  const controlDisabled =
    disabled ||
    saving ||
    loadStatus === "loading" ||
    (loadStatus === "ready" && catalog?.switch_allowed === false);

  useEffect(() => {
    if (disabled) {
      setOpen(false);
      setSearchQuery("");
    }
  }, [disabled]);

  const closeAndRestoreFocus = useCallback(() => {
    setOpen(false);
    setSearchQuery("");
    requestAnimationFrame(() => triggerRef.current?.focus());
  }, []);

  useEffect(() => {
    if (!open || loadStatus !== "ready") return;
    const frame = requestAnimationFrame(() => {
      const searchInput = menuRef.current?.querySelector<HTMLInputElement>(
        ".chat-model-selector-search input",
      );
      const firstAction = menuRef.current?.querySelector<HTMLElement>(
        "button:not([disabled])",
      );
      (searchInput ?? firstAction)?.focus();
    });
    return () => cancelAnimationFrame(frame);
  }, [loadStatus, open]);

  useEffect(() => {
    const handleOpenRequest = (event: Event) => {
      const detail = (
        event as CustomEvent<{ conversationId?: string }>
      ).detail;
      const requestedId = detail?.conversationId?.trim();
      if (requestedId && requestedId !== normalizedConversationId) return;
      if (controlDisabled) {
        message.warning(switchBlockedReason);
        return;
      }
      setOpen(true);
    };
    window.addEventListener(CHAT_OPEN_MODEL_SELECTOR_EVENT, handleOpenRequest);
    return () =>
      window.removeEventListener(
        CHAT_OPEN_MODEL_SELECTOR_EVENT,
        handleOpenRequest,
      );
  }, [controlDisabled, normalizedConversationId, switchBlockedReason]);

  const currentSelection = catalog?.selection;
  const currentProvider = useMemo(
    () => modelProviderForSelection(catalog?.providers ?? [], currentSelection),
    [catalog?.providers, currentSelection],
  );
  const currentModel = currentProvider?.models.find(
    (model) => model.id === currentSelection?.model_id,
  );
  const visibleProviders = useMemo(() => {
    const providers = (catalog?.providers ?? []).filter(
      (provider) => provider.models.length > 0,
    );
    const query = searchQuery.trim().toLocaleLowerCase();
    if (!query) return providers;
    const includesQuery = (value?: string) =>
      value?.toLocaleLowerCase().includes(query) ?? false;
    return providers
      .map((provider) => ({
        ...provider,
        models: includesQuery(provider.name)
          ? provider.models
          : provider.models.filter(
              (model) =>
                includesQuery(model.name) ||
                includesQuery(model.group_name) ||
                model.capabilities?.some(includesQuery) ||
                model.badges?.some(includesQuery),
            ),
      }))
      .filter((provider) => provider.models.length > 0);
  }, [catalog?.providers, searchQuery]);
  const hasProviderModels =
    catalog?.providers.some((provider) => provider.models.length > 0) ?? false;

  const selectionLabel = (() => {
    if (loadStatus === "loading") return t("chat.modelSelectorLoading");
    if (currentSelection?.mode === "auto") {
      return currentSelection.provider_name && currentSelection.model_name
        ? `Auto · ${currentSelection.provider_name} · ${currentSelection.model_name}`
        : "Auto";
    }
    if (currentSelection?.provider_name && currentSelection.model_name) {
      return `${currentSelection.provider_name} · ${currentSelection.model_name}`;
    }
    if (currentProvider && currentModel) {
      return modelLabel(currentProvider, currentModel);
    }
    return loadStatus === "error"
      ? t("chat.modelSelectorUnavailable")
      : t("chat.modelSelectorChoose");
  })();
  const triggerLabel =
    currentSelection?.availability === "unavailable"
      ? `${selectionLabel} · ${t("chat.modelSelectorUnavailable")}`
      : selectionLabel;

  const applySelection = useCallback(
    async (
      request: ChatModelSelectionRequest,
      optimisticSelection: ChatModelSelection,
      successLabel: string,
    ) => {
      if (!catalog || savingRef.current) return;
      const isSameSelection =
        catalog.selection.mode === request.mode &&
        (request.mode === "auto" ||
          catalog.selection.model_id === request.model_id);
      if (isSameSelection) {
        closeAndRestoreFocus();
        return;
      }

      setSwitchError("");
      if (!normalizedConversationId) {
        const nextCatalog = { ...catalog, selection: optimisticSelection };
        setCatalog(nextCatalog);
        commitSelection(optimisticSelection);
        const successMessage = t("chat.modelSelectorSwitched", {
          model: successLabel,
        });
        message.success(successMessage);
        setAnnouncement(successMessage);
        closeAndRestoreFocus();
        return;
      }

      const sequence = ++saveSequenceRef.current;
      saveControllerRef.current?.abort();
      const controller = new AbortController();
      saveControllerRef.current = controller;
      savingRef.current = true;
      setSaving(true);
      onSavingChangeRef.current?.(true);
      setAnnouncement(t("chat.modelSelectorSwitching"));
      try {
        const savedSelection = await updateConversationChatModel(
          normalizedConversationId,
          request,
          catalog.selection.version,
          controller.signal,
        );
        if (controller.signal.aborted || sequence !== saveSequenceRef.current) {
          return;
        }
        const nextSelection = savedSelection ?? {
          ...optimisticSelection,
          version: catalog.selection.version + 1,
        };
        setCatalog((current) =>
          current ? { ...current, selection: nextSelection } : current,
        );
        commitSelection(nextSelection);
        const successMessage = t("chat.modelSelectorSwitched", {
          model: successLabel,
        });
        message.success(successMessage);
        setAnnouncement(successMessage);
        closeAndRestoreFocus();
      } catch {
        if (controller.signal.aborted || sequence !== saveSequenceRef.current) {
          return;
        }
        const errorMessage = t("chat.modelSelectorSwitchFailed");
        setSwitchError(errorMessage);
        setAnnouncement(errorMessage);
      } finally {
        if (saveControllerRef.current === controller) {
          saveControllerRef.current = null;
          savingRef.current = false;
          setSaving(false);
          onSavingChangeRef.current?.(false);
        }
      }
    },
    [
      catalog,
      closeAndRestoreFocus,
      commitSelection,
      normalizedConversationId,
      t,
    ],
  );

  const chooseAuto = () => {
    if (!catalog) return;
    void applySelection(
      { mode: "auto" },
      { mode: "auto", version: catalog.selection.version },
      "Auto",
    );
  };

  const chooseModel = (provider: ChatModelProvider, model: ChatModelOption) => {
    if (!catalog || !isModelAvailable(model)) return;
    void applySelection(
      { mode: "fixed", model_id: model.id },
      {
        mode: "fixed",
        model_id: model.id,
        provider_name: provider.name,
        model_name: model.name,
        group_name: model.group_name,
        source: model.source,
        version: catalog.selection.version,
      },
      modelLabel(provider, model),
    );
  };

  const content = (
    <div
      className="chat-model-selector-menu"
      role="dialog"
      aria-label={t("chat.modelSelectorDialogLabel")}
      aria-busy={saving || loadStatus === "loading"}
      ref={menuRef}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.stopPropagation();
          closeAndRestoreFocus();
        }
      }}
    >
      {loadStatus === "loading" ? (
        <div className="chat-model-selector-state" role="status">
          <Spin size="small" />
          <span>{t("chat.modelSelectorLoading")}</span>
        </div>
      ) : null}

      {loadStatus === "error" ? (
        <div className="chat-model-selector-state" role="alert">
          <span>{t("chat.modelSelectorLoadFailed")}</span>
          <button type="button" onClick={() => void loadCatalog()}>
            <ReloadOutlined aria-hidden="true" />
            {t("chat.modelSelectorRetry")}
          </button>
        </div>
      ) : null}

      {loadStatus === "ready" && catalog ? (
        <>
          {hasProviderModels ? (
            <label className="chat-model-selector-search">
              <SearchOutlined aria-hidden="true" />
              <input
                type="search"
                aria-label={t("chat.modelSelectorSearchLabel")}
                placeholder={t("chat.modelSelectorSearchPlaceholder")}
                value={searchQuery}
                disabled={saving}
                onChange={(event) => setSearchQuery(event.target.value)}
              />
            </label>
          ) : null}

          {catalog.auto_available || currentSelection?.mode === "auto" ? (
            <button
              type="button"
              className={`chat-model-auto-option${
                currentSelection?.mode === "auto" ? " is-current" : ""
              }`}
              aria-pressed={currentSelection?.mode === "auto"}
              aria-label={`Auto，${t("chat.modelSelectorAutoDescription")}`}
              data-current={currentSelection?.mode === "auto"}
              title={t("chat.modelSelectorAutoDescription")}
              disabled={saving || !catalog.auto_available}
              onClick={chooseAuto}
            >
              <strong>Auto</strong>
              {!catalog.auto_available ? (
                <small className="chat-model-option-unavailable">
                  {t("chat.modelSelectorUnavailable")}
                </small>
              ) : null}
              {currentSelection?.mode === "auto" ? (
                <CheckOutlined aria-hidden="true" />
              ) : null}
            </button>
          ) : null}

          {hasProviderModels ? (
            <div className="chat-model-selector-body">
              {visibleProviders.length > 0 ? (
                visibleProviders.map((provider) => (
                  <section
                    className="chat-model-provider-group"
                    aria-label={provider.name}
                    key={providerKey(provider)}
                  >
                    <div className="chat-model-provider-heading">
                      <span>{provider.name}</span>
                      {provider.source === "shared" ? (
                        <small>{t("chat.modelSelectorShared")}</small>
                      ) : null}
                    </div>
                    {provider.models.map((model) => {
                      const isCurrent =
                        currentSelection?.mode === "fixed" &&
                        (currentSelection.model_id
                          ? currentSelection.model_id === model.id
                          : model.current === true);
                      const rawBadges = new Set(model.badges ?? []);
                      const badges = [
                        model.default ||
                        model.is_default ||
                        rawBadges.has("default")
                          ? t("chat.modelSelectorDefault")
                          : "",
                        model.is_recommended || rawBadges.has("recommended")
                          ? t("chat.modelSelectorRecommended")
                          : "",
                        model.is_low_cost ||
                        rawBadges.has("low_cost") ||
                        rawBadges.has("low-cost")
                          ? t("chat.modelSelectorLowCost")
                          : "",
                        model.shared ||
                        model.is_shared ||
                        model.source === "shared" ||
                        rawBadges.has("shared")
                          ? t("chat.modelSelectorShared")
                          : "",
                      ].filter(Boolean);
                      return (
                        <button
                          type="button"
                          className={`chat-model-option${
                            isCurrent ? " is-current" : ""
                          }`}
                          key={model.id}
                          aria-pressed={isCurrent}
                          data-current={isCurrent}
                          disabled={saving || !isModelAvailable(model)}
                          onClick={() => chooseModel(provider, model)}
                        >
                          <span className="chat-model-option-copy">
                            <strong>{model.name}</strong>
                            {model.group_name &&
                            model.group_name !== provider.name ? (
                              <small>{model.group_name}</small>
                            ) : null}
                            {model.capabilities?.length ? (
                              <small>
                                {model.capabilities
                                  .map((capability) =>
                                    capability === "chat"
                                      ? t("chat.modelSelectorCapabilityChat")
                                      : capability,
                                  )
                                  .join(" / ")}
                              </small>
                            ) : null}
                            {badges.length ? (
                              <span className="chat-model-option-badges">
                                {badges.map((badge) => (
                                  <em key={badge}>{badge}</em>
                                ))}
                              </span>
                            ) : null}
                            {!isModelAvailable(model) ? (
                              <small className="chat-model-option-unavailable">
                                {t("chat.modelSelectorUnavailable")}
                              </small>
                            ) : null}
                          </span>
                          {isCurrent ? (
                            <CheckOutlined aria-hidden="true" />
                          ) : null}
                        </button>
                      );
                    })}
                  </section>
                ))
              ) : (
                <div className="chat-model-selector-empty" role="status">
                  {t("chat.modelSelectorSearchEmpty")}
                </div>
              )}
            </div>
          ) : !catalog.auto_available ? (
            <div className="chat-model-selector-empty" role="status">
              {t("chat.modelSelectorEmpty")}
            </div>
          ) : null}

          {switchError ? (
            <div className="chat-model-selector-error" role="alert">
              <span>{switchError}</span>
              <button type="button" onClick={() => void loadCatalog()}>
                {t("chat.modelSelectorReload")}
              </button>
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  );

  const trigger = (
    <button
      ref={triggerRef}
      type="button"
      className={`chat-model-selector-trigger${
        controlDisabled ? " is-disabled" : ""
      }`}
      aria-label={t("chat.modelSelectorTriggerLabel", { model: triggerLabel })}
      aria-haspopup="dialog"
      aria-expanded={open}
      aria-disabled={controlDisabled}
      aria-describedby={controlDisabled ? disabledReasonId : undefined}
      onClick={(event) => {
        if (!controlDisabled) return;
        event.preventDefault();
        event.stopPropagation();
        message.warning(switchBlockedReason);
      }}
    >
      {loadStatus === "loading" ? <Spin size="small" /> : null}
      <span>{triggerLabel}</span>
      <DownOutlined aria-hidden="true" />
    </button>
  );

  return (
    <div className="chat-model-selector">
      <Popover
        arrow={false}
        content={content}
        destroyOnHidden
        open={open}
        overlayClassName="chat-model-selector-popover"
        placement="topLeft"
        trigger="click"
        onOpenChange={(nextOpen: boolean) => {
          if (nextOpen && controlDisabled) return;
          if (!nextOpen) setSearchQuery("");
          setOpen(nextOpen);
        }}
      >
        <Tooltip title={controlDisabled ? switchBlockedReason : undefined}>
          <span className="chat-model-selector-trigger-wrap">{trigger}</span>
        </Tooltip>
      </Popover>
      <span className="chat-model-selector-live" aria-live="polite">
        {announcement}
      </span>
      <span id={disabledReasonId} className="chat-model-selector-live">
        {controlDisabled ? switchBlockedReason : ""}
      </span>
    </div>
  );
};

export default ChatModelSelector;
