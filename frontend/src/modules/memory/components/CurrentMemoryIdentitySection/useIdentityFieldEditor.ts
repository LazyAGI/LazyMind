import { message } from "antd";
import {
  type Dispatch,
  type SetStateAction,
  useCallback,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import type { CurrentMemorySnapshot } from "../../currentMemoryApi";
import { isCurrentMemoryConflict } from "../../currentMemoryViewModel";

export type IdentityFieldValue = string | null | string[];

export interface IdentityField<TPatch> {
  path: string;
  label: string;
  value: IdentityFieldValue;
  valueType: "required-string" | "nullable-string" | "string-list";
  buildPatch: (value: IdentityFieldValue) => TPatch;
}

interface UseIdentityFieldEditorOptions<TDocument, TPatch> {
  kind: "soul" | "profile";
  load: () => Promise<CurrentMemorySnapshot<TDocument>>;
  save: (patch: TPatch) => Promise<CurrentMemorySnapshot<TDocument>>;
  setSnapshot: Dispatch<
    SetStateAction<CurrentMemorySnapshot<TDocument> | null>
  >;
}

const normalizeDraftValue = <TPatch,>(
  field: IdentityField<TPatch>,
  value: IdentityFieldValue,
): IdentityFieldValue => {
  if (field.valueType === "required-string") {
    return String(value || "").trim();
  }
  if (field.valueType === "nullable-string") {
    return String(value || "").trim() || null;
  }
  return Array.isArray(value)
    ? value.map((item) => item.trim()).filter(Boolean)
    : [];
};

export const useIdentityFieldEditor = <TDocument, TPatch>({
  kind,
  load,
  save,
  setSnapshot,
}: UseIdentityFieldEditorOptions<TDocument, TPatch>) => {
  const { t } = useTranslation();
  const [editingField, setEditingField] =
    useState<IdentityField<TPatch> | null>(null);
  const [draftValue, setDraftValue] = useState<IdentityFieldValue>("");
  const draftValueRef = useRef<IdentityFieldValue>("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [conflict, setConflict] = useState(false);

  const updateDraftValue = useCallback((value: IdentityFieldValue) => {
    draftValueRef.current = value;
    setDraftValue(value);
  }, []);

  const beginEdit = useCallback((field: IdentityField<TPatch>) => {
    setEditingField(field);
    updateDraftValue(
      Array.isArray(field.value) ? [...field.value] : field.value,
    );
    setSaveError("");
    setConflict(false);
  }, [updateDraftValue]);

  const clearEditor = useCallback(() => {
    setEditingField(null);
    updateDraftValue("");
    setSaveError("");
    setConflict(false);
  }, [updateDraftValue]);

  const cancelEdit = useCallback(() => {
    if (!saving) {
      clearEditor();
    }
  }, [clearEditor, saving]);

  const saveField = useCallback(async () => {
    if (!editingField || saving) {
      return;
    }
    const nextValue = normalizeDraftValue(
      editingField,
      draftValueRef.current,
    );
    if (
      editingField.valueType === "required-string" &&
      !String(nextValue).trim()
    ) {
      setSaveError(t("admin.memoryCurrentRequiredField"));
      return;
    }

    setSaving(true);
    setSaveError("");
    setConflict(false);
    try {
      setSnapshot(await save(editingField.buildPatch(nextValue)));
      clearEditor();
      message.success(t("admin.memoryCurrentSaveSuccess"));
    } catch (error) {
      console.error(`Save ${kind} memory field failed:`, error);
      if (isCurrentMemoryConflict(error)) {
        setConflict(true);
      } else {
        setSaveError(getLocalizedErrorMessage(error));
      }
    } finally {
      setSaving(false);
    }
  }, [
    clearEditor,
    editingField,
    kind,
    save,
    saving,
    setSnapshot,
    t,
  ]);

  const reloadConflictSnapshot = useCallback(async () => {
    if (saving) {
      return;
    }
    setSaving(true);
    setSaveError("");
    try {
      setSnapshot(await load());
      setConflict(false);
    } catch (error) {
      setSaveError(getLocalizedErrorMessage(error));
    } finally {
      setSaving(false);
    }
  }, [load, saving, setSnapshot]);

  return {
    beginEdit,
    cancelEdit,
    conflict,
    draftValue,
    editingField,
    reloadConflictSnapshot,
    saveError,
    saveField,
    saving,
    updateDraftValue,
  };
};
