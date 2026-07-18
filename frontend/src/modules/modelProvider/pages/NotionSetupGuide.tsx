import { useRef } from "react";
import { Button, Typography } from "antd";
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import { getCloudDataSourceCallbackUrl } from "@/modules/dataSource/oauth/urls";
import "./feishuSetupGuide.scss";
import { CLOUD_DOCUMENTS_PATH } from "../utils/cloudDocumentUrls";

const { Paragraph, Text } = Typography;

const NOTION_DEVELOPERS_URL = "https://app.notion.com/developers/connections";

type GuideStep = {
  title: string;
  description: string;
  details?: string[];
  linkLabel?: string;
  linkHref?: string;
};

function buildGuideSteps(t: TFunction, redirectUri: string): GuideStep[] {
  const stepKey = (key: string) => `admin.dataSourceNotionSetupGuide.steps.${key}`;
  return [
    {
      title: t(stepKey("openDevelopersTitle")),
      description: t(stepKey("openDevelopersDesc")),
      linkLabel: t("admin.dataSourceNotionSetupGuide.openDevelopers"),
      linkHref: NOTION_DEVELOPERS_URL,
    },
    {
      title: t(stepKey("createIntegrationTitle")),
      description: t(stepKey("createIntegrationDesc")),
      details: [
        t(stepKey("createIntegrationExisting")),
        t(stepKey("createIntegrationNaming")),
      ],
    },
    {
      title: t(stepKey("basicInfoTitle")),
      description: t(stepKey("basicInfoDesc")),
    },
    {
      title: t(stepKey("credentialsTitle")),
      description: t(stepKey("credentialsDesc")),
      details: [
        t(stepKey("credentialsClientId")),
        t(stepKey("credentialsClientSecret")),
      ],
    },
    {
      title: t(stepKey("redirectTitle")),
      description: t(stepKey("redirectDesc")),
      details: [
        t("admin.dataSourceNotionSetupGuide.callbackUrl", {
          uri: redirectUri,
        }),
        t(stepKey("redirectProductionHint")),
      ],
    },
    {
      title: t(stepKey("capabilitiesTitle")),
      description: t(stepKey("capabilitiesDesc")),
      details: [
        t(stepKey("capabilitiesRequired")),
        t(stepKey("capabilitiesWrite")),
      ],
    },
    {
      title: t(stepKey("enterCredentialsTitle")),
      description: t(stepKey("enterCredentialsDesc")),
      details: [
        t(stepKey("enterCredentialsClientId")),
        t(stepKey("enterCredentialsClientSecret")),
      ],
    },
    {
      title: t(stepKey("finishTitle")),
      description: t(stepKey("finishDesc")),
      details: [
        t(stepKey("finishPageLink")),
        t(stepKey("finishDatabaseLink")),
        t(stepKey("finishCopyLink")),
        t(stepKey("finishConnectionNote")),
      ],
    },
  ];
}

export default function NotionSetupGuide() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const pageRef = useRef<HTMLDivElement | null>(null);
  const headerRef = useRef<HTMLElement | null>(null);
  const stepRefs = useRef<Array<HTMLElement | null>>([]);
  const isFromCreateSource =
    new URLSearchParams(location.search).get("from") === "create-source";
  const orderedGuideSteps = buildGuideSteps(
    t,
    getCloudDataSourceCallbackUrl("notion"),
  );

  const scrollToStep = (index: number) => {
    const page = pageRef.current;
    const target = stepRefs.current[index];

    if (!page || !target) {
      return;
    }

    const pageRect = page.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    const headerHeight = headerRef.current?.getBoundingClientRect().height || 0;

    page.scrollTo({
      top: page.scrollTop + targetRect.top - pageRect.top - headerHeight - 12,
      behavior: "smooth",
    });
  };

  return (
    <div className="feishu-setup-guide-page" ref={pageRef}>
      <header className="feishu-setup-guide-header" ref={headerRef}>
        <div>
          <Button
            type="link"
            icon={<ArrowLeftOutlined />}
            className="feishu-setup-guide-back"
            onClick={() =>
              navigate(isFromCreateSource ? "/data-sources" : CLOUD_DOCUMENTS_PATH)
            }
          >
            {isFromCreateSource
              ? t("admin.dataSourceNotionSetupGuide.backCreateSource")
              : t("admin.dataSourceNotionSetupGuide.backManagement")}
          </Button>
          <h1>{t("admin.dataSourceNotionSetupGuide.title")}</h1>
          <Paragraph className="feishu-setup-guide-subtitle">
            {t("admin.dataSourceNotionSetupGuide.subtitle")}
          </Paragraph>
        </div>
      </header>

      <main className="feishu-setup-guide-shell">
        <aside
          className="feishu-setup-guide-summary"
          aria-label={t("admin.dataSourceNotionSetupGuide.summaryAria")}
        >
          <Text strong>{t("admin.dataSourceNotionSetupGuide.summaryTitle")}</Text>
          <ol>
            {orderedGuideSteps.map((step, index) => (
              <li key={step.title}>
                <button type="button" onClick={() => scrollToStep(index)}>
                  {step.title}
                </button>
              </li>
            ))}
          </ol>
        </aside>

        <section className="feishu-setup-guide-content">
          {orderedGuideSteps.map((step, index) => (
            <article
              className="feishu-setup-guide-step"
              id={`notion-setup-step-${index + 1}`}
              key={step.title}
              ref={(node) => {
                stepRefs.current[index] = node;
              }}
            >
              <div className="feishu-setup-guide-step-copy">
                <span className="feishu-setup-guide-step-index">
                  {String(index + 1).padStart(2, "0")}
                </span>
                <div>
                  <h2>{step.title}</h2>
                  <Paragraph>
                    {"linkLabel" in step && step.linkLabel ? (
                      <>
                        <a
                          className="feishu-setup-guide-inline-link"
                          href={step.linkHref}
                          target="_blank"
                          rel="noreferrer"
                        >
                          {step.linkLabel}
                        </a>
                        ，
                      </>
                    ) : null}
                    {step.description}
                  </Paragraph>
                  {"details" in step && step.details ? (
                    <ul className="feishu-setup-guide-step-details">
                      {step.details.map((detail) => (
                        <li key={detail}>{detail}</li>
                      ))}
                    </ul>
                  ) : null}
                </div>
                <CheckCircleOutlined className="feishu-setup-guide-step-icon" />
              </div>
            </article>
          ))}
        </section>
      </main>
    </div>
  );
}
