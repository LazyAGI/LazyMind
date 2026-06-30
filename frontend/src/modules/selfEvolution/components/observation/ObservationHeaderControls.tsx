import { Button } from "antd";
import { ArrowLeftOutlined, MenuUnfoldOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { ObservationHeaderControlsProps } from "./types";

export function ObservationHeaderControls({
  isMenuCollapsed,
  toggleMenu,
  onBack,
}: ObservationHeaderControlsProps) {
  const { t } = useTranslation();
  return (
    <div className="self-evolution-observation-head-controls">
      {isMenuCollapsed && toggleMenu ? (
        <Button
          type="text"
          icon={<MenuUnfoldOutlined />}
          onClick={toggleMenu}
          aria-label={t("selfEvolutionRun.observation.expandMenu")}
          title={t("selfEvolutionRun.observation.expandMenu")}
        />
      ) : null}
      <Button type="text" icon={<ArrowLeftOutlined />} onClick={onBack}>
        {t("selfEvolutionRun.observation.back")}
      </Button>
    </div>
  );
}
