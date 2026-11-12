import { Typography } from "antd";
import { useTranslation } from "react-i18next";
import type { CloudVocabularyProvider } from "./cloudApi";

const tokenSources = {
  eudic: "https://my.eudic.net/OpenAPI/Authorization",
  maimemo: "https://open.maimemo.com/",
};

export function CloudTokenSource({ provider }: { provider: CloudVocabularyProvider }) {
  const { t } = useTranslation();
  return provider === "eudic" ? (
    <>
      <a href={tokenSources.eudic} target="_blank" rel="noopener noreferrer">{t("vocabularyCloud.eudicTokenLink")}</a>
      {" "}{t("vocabularyCloud.eudicTokenHelp")}
    </>
  ) : (
    <>
      {t("vocabularyCloud.maimemoTokenHelp")}{" "}
      <a href={tokenSources.maimemo} target="_blank" rel="noopener noreferrer">{t("vocabularyCloud.maimemoDocs")}</a>
    </>
  );
}

export default function VocabularyConnectionGuide(
  props: { provider: CloudVocabularyProvider } | { provider: "anki"; addonCode: string },
) {
  const { t } = useTranslation();
  return (
    <details className="vocabulary-cloud-details">
      <summary>{t("vocabularyCloud.setupGuide")}</summary>
      {props.provider === "anki" ? (
        <>
          <Typography.Paragraph type="secondary">{t("ankiSettings.guideAvailability")}</Typography.Paragraph>
          <ol>
            <li>
              <a href="https://apps.ankiweb.net/" target="_blank" rel="noopener noreferrer">{t("ankiSettings.downloadAnki")}</a>
              {" "}{t("ankiSettings.launchGuide")}
            </li>
            <li>
              {t("ankiSettings.guideAddon", { code: props.addonCode })}{" "}
              <a href={`https://ankiweb.net/shared/info/${props.addonCode}`} target="_blank" rel="noopener noreferrer">AnkiConnect</a>
            </li>
            <li>{t("ankiSettings.guideEnable")}</li>
          </ol>
        </>
      ) : (
        <ol>
          <li><CloudTokenSource provider={props.provider} /></li>
          <li>{t("vocabularyCloud.guideVerify")}</li>
          <li>{t("vocabularyCloud.guideTarget")}</li>
        </ol>
      )}
      <Typography.Paragraph type="secondary">{t("vocabularyCloud.guideLocal")}</Typography.Paragraph>
    </details>
  );
}
