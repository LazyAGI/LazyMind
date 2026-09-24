import { Button, Dropdown } from "antd";
import { DownOutlined } from "@ant-design/icons";
import type { MenuProps } from "antd";
import type { SkillCallMode } from "../../skillApi";

interface Props {
  value: SkillCallMode;
  disabled?: boolean;
  t: (key: string) => string;
  onChange: (value: SkillCallMode) => void;
}

export default function SkillCallModeControl({ value, disabled, t, onChange }: Props) {
  const labels: Record<SkillCallMode, string> = {
    priority: t("admin.memorySkillCallModePriority"),
    on_demand: t("admin.memorySkillCallModeOnDemand"),
    manual: t("admin.memorySkillCallModeManual"),
  };
  return (
    <Dropdown
      overlayClassName="memory-skill-call-mode-menu"
      disabled={disabled}
      trigger={["click"]}
      menu={{
        selectable: true,
        selectedKeys: [value],
        items: getSkillCallModeMenuItems(t),
        onClick: ({ key }) => onChange(key as SkillCallMode),
      }}
    >
      <Button
        className={`memory-skill-call-mode-select is-${value}`}
        disabled={disabled}
        aria-label={`${t("admin.memorySkillCallMode")}: ${labels[value]}`}
      >
        {labels[value]}
        <DownOutlined aria-hidden="true" />
      </Button>
    </Dropdown>
  );
}

export function getSkillCallModeMenuItems(t: (key: string) => string): MenuProps["items"] {
  return (["priority", "on_demand", "manual"] as const).map((mode) => ({
    key: mode,
    label: (
      <span className="memory-skill-call-mode-option">
        <strong>{t(`admin.memorySkillCallMode${mode === "on_demand" ? "OnDemand" : mode === "priority" ? "Priority" : "Manual"}`)}</strong>
        <small>{t(`admin.memorySkillCallMode${mode === "on_demand" ? "OnDemand" : mode === "priority" ? "Priority" : "Manual"}Desc`)}</small>
      </span>
    ),
  }));
}
