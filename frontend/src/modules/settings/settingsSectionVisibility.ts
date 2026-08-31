import { runtimeFeatures, type RuntimeFeatures } from "@/runtime/features";

type SettingsVisibilityFeatures = Pick<
  RuntimeFeatures,
  "hideEvo" | "hideUserGroupSurfaces"
>;

export function isSettingsSectionVisible(
  section: "organization" | "developer",
  isAdmin: boolean,
  features: SettingsVisibilityFeatures = runtimeFeatures,
) {
  if (section === "organization") {
    return isAdmin && !features.hideUserGroupSurfaces;
  }

  return !features.hideEvo;
}
