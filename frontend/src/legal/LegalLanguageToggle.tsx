import { Select } from 'antd';
import { useTranslation } from 'react-i18next';
import { LANGUAGES } from '@/i18n';
import './LegalLanguageToggle.scss';

export default function LegalLanguageToggle() {
  const { i18n } = useTranslation();

  const handleChange = (value: string) => {
    void i18n.changeLanguage(value);
    localStorage.setItem('i18n_language', value);
  };

  return (
    <Select
      value={i18n.language}
      onChange={handleChange}
      options={LANGUAGES}
      size="small"
      className="legal-language-toggle"
      popupMatchSelectWidth={false}
      getPopupContainer={() => document.body}
      aria-label="Language"
    />
  );
}
