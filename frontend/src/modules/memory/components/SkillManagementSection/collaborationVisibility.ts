export function shouldShowSkillMessageCenter({
  skillView,
  hideUserGroupSurfaces,
}: {
  skillView: "installed" | "market" | "workflows";
  hideUserGroupSurfaces: boolean;
}) {
  return skillView === "installed" && !hideUserGroupSurfaces;
}
