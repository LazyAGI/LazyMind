import type { TFunction } from "i18next";
import type { CustomLogoPickerCopy } from "./components/CustomLogoPicker";

export function getCustomLogoCopy(t: TFunction): CustomLogoPickerCopy {
  const text = (key: string) => t(`modelProvider.logoUpload.${key}`);
  return {
    label: text("label"),
    upload: text("upload"),
    replace: text("replace"),
    reset: text("reset"),
    hint: text("hint"),
    errors: {
      empty: text("errors.empty"),
      type: text("errors.type"),
      size: text("errors.size"),
      decode: text("errors.decode"),
      output: text("errors.output"),
    },
  };
}
