import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import zhCN from "./locales/zh-CN";
import enUS from "./locales/en-US";
import {
  knowledgeTerminologyPostProcessor,
  subscribeKnowledgeTerminologyChange,
} from "./knowledgeTerminology";

export const LANGUAGES = [
  { value: "zh-CN", label: "中文" },
  { value: "en-US", label: "English" },
];

export const DEFAULT_LANGUAGE = "zh-CN";

const storedLang = localStorage.getItem("i18n_language");
const supportedLngs = LANGUAGES.map((l) => l.value);
const initialLng =
  storedLang && supportedLngs.includes(storedLang)
    ? storedLang
    : DEFAULT_LANGUAGE;

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .use(knowledgeTerminologyPostProcessor)
  .init({
    resources: {
      "zh-CN": { translation: zhCN },
      "en-US": { translation: enUS },
    },
    fallbackLng: DEFAULT_LANGUAGE,
    lng: initialLng,
    supportedLngs,
    nonExplicitSupportedLngs: false,
    interpolation: {
      escapeValue: false,
    },
    postProcess: [knowledgeTerminologyPostProcessor.name],
    detection: {
      order: ["localStorage", "navigator"],
      lookupLocalStorage: "i18n_language",
      caches: ["localStorage"],
    },
  });

subscribeKnowledgeTerminologyChange(() => {
  void i18n.changeLanguage(i18n.language);
});

export default i18n;
