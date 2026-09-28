export function shouldShowSkillMessageCenter({
  skillView,
}: {
  skillView: "installed" | "market" | "workflows";
}) {
  return skillView === "installed";
}
