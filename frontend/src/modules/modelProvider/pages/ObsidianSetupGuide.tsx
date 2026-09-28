import { useRef } from "react";
import { Button, Typography } from "antd";
import { ArrowLeftOutlined, CheckCircleOutlined } from "@ant-design/icons";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import "./feishuSetupGuide.scss";
import { CLOUD_DOCUMENTS_PATH } from "../utils/cloudDocumentUrls";

const { Paragraph, Text } = Typography;

type GuideStep = {
  title: string;
  description: string;
  details?: string[];
  fields?: Array<[string, string]>;
};

function buildGuideSteps(t: TFunction): GuideStep[] {
  const stepKey = (key: string) => `admin.dataSourceObsidianSetupGuide.steps.${key}`;
  const fieldKey = (key: string) => `${stepKey("dockerFields")}.${key}`;
  return [
    {
      title: t(stepKey("vaultTitle")),
      description: t(stepKey("vaultDesc")),
      details: t(stepKey("vaultDetails"), { returnObjects: true }) as string[],
    },
    {
      title: t(stepKey("dockerTitle")),
      description: t(stepKey("dockerDesc")),
      fields: [
        [t(fieldKey("mac")), t(fieldKey("macValue"))],
        [t(fieldKey("windows")), t(fieldKey("windowsValue"))],
      ],
      details: t(stepKey("dockerDetails"), { returnObjects: true }) as string[],
    },
    {
      title: t(stepKey("desktopTitle")),
      description: t(stepKey("desktopDesc")),
      details: t(stepKey("desktopDetails"), { returnObjects: true }) as string[],
    },
    {
      title: t(stepKey("verifyTitle")),
      description: t(stepKey("verifyDesc")),
      details: t(stepKey("verifyDetails"), { returnObjects: true }) as string[],
    },
  ];
}

export default function ObsidianSetupGuide() {
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
    <div className="feishu-setup-guide-page" ref={pageRef}>
      <header className="feishu-setup-guide-header" ref={headerRef}>
        <div>
          <Button
            type="link"
            icon={<ArrowLeftOutlined />}
            className="feishu-setup-guide-back"
            onClick={() => navigate(CLOUD_DOCUMENTS_PATH)}
          >
            {t("admin.dataSourceObsidianSetupGuide.backManagement")}
          </Button>
          <h1>{t("admin.dataSourceObsidianSetupGuide.title")}</h1>
          <Paragraph className="feishu-setup-guide-subtitle">
            {t("admin.dataSourceObsidianSetupGuide.subtitle")}
          </Paragraph>
        </div>
      </header>

      <main className="feishu-setup-guide-shell">
        <aside
          className="feishu-setup-guide-summary"
          aria-label={t("admin.dataSourceObsidianSetupGuide.summaryAria")}
        >
          <Text strong>{t("admin.dataSourceObsidianSetupGuide.summaryTitle")}</Text>
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
              id={`obsidian-setup-step-${index + 1}`}
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
                  <Paragraph>{step.description}</Paragraph>
                  {step.fields ? (
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">{t("admin.dataSourceObsidianSetupGuide.field")}</th>
                          <th scope="col">{t("admin.dataSourceObsidianSetupGuide.value")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {step.fields.map(([label, value]) => (
                          <tr key={label}>
                            <th scope="row">{label}</th>
                            <td><Text code copyable>{value}</Text></td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  ) : null}
                  {step.details ? (
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
