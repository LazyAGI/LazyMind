import { runtimeFeatures, type RuntimeFeatures } from "@/runtime/features";

type SettingsVisibilityFeatures = Pick<
  RuntimeFeatures,
  "hideUserGroupSurfaces"
>;

export function isSettingsSectionVisible(
  section: "organization" | "developer",
  isAdmin: boolean,
  features: SettingsVisibilityFeatures = runtimeFeatures,
) {
  if (section === "organization") {
    return isAdmin && !features.hideUserGroupSurfaces;
  }

  return true;
}
