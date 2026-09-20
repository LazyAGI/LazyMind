import { useEffect, useState } from "react";
import { Slider } from "antd";
import type { SkillCallMode } from "../../skillApi";

interface Props {
  value: SkillCallMode;
  disabled?: boolean;
  t: (key: string) => string;
  onChange: (value: SkillCallMode) => void;
}

export default function SkillCallModeControl({ value, disabled, t, onChange }: Props) {
  const selectedPosition = value === "manual" ? 0 : value === "priority" ? 2 : 1;
  const [position, setPosition] = useState(selectedPosition);
  useEffect(() => setPosition(selectedPosition), [selectedPosition, disabled]);
  const labels = [
    t("admin.memorySkillCallModeManual"),
    t("admin.memorySkillCallModeOnDemand"),
    t("admin.memorySkillCallModePriority"),
  ];
  return (
    <Slider
      className="memory-skill-call-mode-slider"
      min={0}
      max={2}
      step={1}
      included={false}
      value={position}
      onChange={setPosition}
      disabled={disabled}
      ariaLabelForHandle={t("admin.memorySkillCallMode")}
      tooltip={{ formatter: (position: number | undefined) => labels[position ?? 1] }}
      marks={{ 0: labels[0], 1: labels[1], 2: labels[2] }}
      onChangeComplete={(position: number) => onChange(position === 0 ? "manual" : position === 2 ? "priority" : "on_demand")}
    />
  );
}
