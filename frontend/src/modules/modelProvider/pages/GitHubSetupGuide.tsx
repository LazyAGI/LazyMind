import { useRef } from "react";
import { Button, Typography } from "antd";
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import "./feishuSetupGuide.scss";
import "./githubSetupGuide.scss";
import { CLOUD_DOCUMENTS_PATH } from "../utils/cloudDocumentUrls";

const { Paragraph, Text } = Typography;

const GITHUB_DEVELOPERS_URL = "https://github.com/settings/applications/new";

type GuideStep = {
  title: string;
  description: string;
  details?: string[];
  fields?: Array<[string, string]>;
  linkLabel?: string;
  linkHref?: string;
};

function buildGuideSteps(t: TFunction): GuideStep[] {
  const stepKey = (key: string) => `admin.dataSourceGitHubSetupGuide.steps.${key}`;
  return [
    {
      title: t(stepKey("createTitle")),
      description: t(stepKey("createDesc")),
      linkLabel: t("admin.dataSourceGitHubSetupGuide.openDevelopers"),
      linkHref: GITHUB_DEVELOPERS_URL,
    },
    {
      title: t(stepKey("applicationTitle")),
      description: t(stepKey("applicationDesc")),
      fields: [
        ["Application name", "LazyMind GitHub"],
        ["Homepage URL", "http://localhost:8090"],
        ["Redirect URI", "http://localhost:8090/oauth/github/data-source/callback"],
      ],
      details: [t(stepKey("portHint")), t(stepKey("registerHint"))],
    },
    {
      title: t(stepKey("credentialsTitle")),
      description: t(stepKey("credentialsDesc")),
      details: [
        t(stepKey("credentialsClientId")),
        t(stepKey("credentialsClientSecret")),
        t(stepKey("credentialsFillBack")),
      ],
    },
    {
      title: t(stepKey("finishTitle")),
      description: t(stepKey("finishDesc")),
      details: [t(stepKey("organizationHint"))],
    },
  ];
}

export default function GitHubSetupGuide() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const pageRef = useRef<HTMLDivElement | null>(null);
  const headerRef = useRef<HTMLElement | null>(null);
  const stepRefs = useRef<Array<HTMLElement | null>>([]);
  const orderedGuideSteps = buildGuideSteps(t);

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
    <div className="feishu-setup-guide-page github-setup-guide-page" ref={pageRef}>
      <header className="feishu-setup-guide-header" ref={headerRef}>
        <div>
          <Button
            type="link"
            icon={<ArrowLeftOutlined />}
            className="feishu-setup-guide-back"
            onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}
          >
            {t("admin.dataSourceGitHubSetupGuide.backManagement")}
          </Button>
          <h1>{t("admin.dataSourceGitHubSetupGuide.title")}</h1>
          <Paragraph className="feishu-setup-guide-subtitle">
            {t("admin.dataSourceGitHubSetupGuide.subtitle")}
          </Paragraph>
        </div>
      </header>

      <main className="feishu-setup-guide-shell">
        <aside
          className="feishu-setup-guide-summary"
          aria-label={t("admin.dataSourceGitHubSetupGuide.summaryAria")}
        >
          <Text strong>{t("admin.dataSourceGitHubSetupGuide.summaryTitle")}</Text>
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
              id={`github-setup-step-${index + 1}`}
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
                  {step.fields ? (
                    <table className="github-setup-guide-fields">
                      <thead>
                        <tr>
                          <th scope="col">{t("admin.dataSourceGitHubSetupGuide.field")}</th>
                          <th scope="col">{t("admin.dataSourceGitHubSetupGuide.value")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {step.fields.map(([label, value]) => (
                          <tr key={label}>
                            <th scope="row">{label}</th>
                            <td><Text copyable>{value}</Text></td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  ) : null}
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
