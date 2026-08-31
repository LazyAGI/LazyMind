import { Select } from "antd";
import { useTranslation } from "react-i18next";
import { LANGUAGES } from "@/i18n";
import "./index.scss";

const LanguageSwitcher = () => {
  const { i18n, t } = useTranslation();

  const handleChange = (value: string) => {
    i18n.changeLanguage(value);
    localStorage.setItem("i18n_language", value);
  };

  return (
    <Select
      value={i18n.language}
      onChange={handleChange}
      options={LANGUAGES}
      size="small"
      className="language-switcher"
      popupMatchSelectWidth={false}
      aria-label={t("layout.language")}
    />
  );
};

export default LanguageSwitcher;
