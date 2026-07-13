export function shouldShowSkillMessageCenter({
  skillView,
  hideUserGroupSurfaces,
}: {
  skillView: "installed" | "market" | "plugins";
  hideUserGroupSurfaces: boolean;
}) {
  return skillView === "installed" && !hideUserGroupSurfaces;
}
