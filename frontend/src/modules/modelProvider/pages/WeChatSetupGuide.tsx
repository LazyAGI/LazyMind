import { useRef, useState } from "react";
import { Button, Image, Typography } from "antd";
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
  FileImageOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import "./feishuSetupGuide.scss";
import "./wechatSetupGuide.scss";
import {
  CLOUD_DOCUMENTS_WECHAT_OFFICIAL_ACCOUNT_PATH,
  WECHAT_OFFICIAL_ACCOUNT_PLATFORM_URL,
} from "../utils/cloudDocumentUrls";

const { Paragraph, Text } = Typography;

type GuideStep = {
  title: string;
  description: string;
  imagePath: string;
  alt: string;
  additionalImage?: { path: string; alt: string };
  details?: string[];
  linkLabel?: string;
  linkHref?: string;
};

function buildGuideSteps(t: TFunction): GuideStep[] {
  const stepKey = (key: string) => `modelProvider.wechatOfficialAccountSetupGuide.steps.${key}`;
  return [
    {
      title: t(stepKey("loginTitle")),
      description: t(stepKey("loginDesc")),
      linkLabel: t("modelProvider.wechatOfficialAccountSetupGuide.openPlatform"),
      linkHref: WECHAT_OFFICIAL_ACCOUNT_PLATFORM_URL,
      imagePath: "/docs/wechat-setup/step-01.png",
      alt: t(stepKey("loginAlt")),
    },
    {
      title: t(stepKey("credentialsTitle")),
      description: t(stepKey("credentialsDesc")),
      details: [t(stepKey("credentialsDetail"))],
      imagePath: "/docs/wechat-setup/step-02.png",
      alt: t(stepKey("credentialsAlt")),
    },
    {
      title: t(stepKey("accountSetupTitle")),
      description: t(stepKey("accountSetupDesc")),
      imagePath: "/docs/wechat-setup/step-03-account.png",
      alt: t(stepKey("accountSetupAlt")),
      additionalImage: {
        path: "/docs/wechat-setup/step-03-egress-ip.png",
        alt: t(stepKey("egressIpAlt")),
      },
    },
    {
      title: t(stepKey("ipWhitelistTitle")),
      description: t(stepKey("ipWhitelistDesc")),
      details: [t(stepKey("ipWhitelistDetail"))],
      imagePath: "/docs/wechat-setup/step-03.png",
      alt: t(stepKey("ipWhitelistAlt")),
    },
    {
      title: t(stepKey("enableWritingTitle")),
      description: t(stepKey("enableWritingDesc")),
      details: [t(stepKey("enableWritingDetail"))],
      imagePath: "/docs/wechat-setup/step-04.png",
      alt: t(stepKey("enableWritingAlt")),
    },
  ];
}

function GuideImage({
  path,
  alt,
  zoomMask,
  placeholderTitle,
}: {
  path: string;
  alt: string;
  zoomMask: string;
  placeholderTitle: string;
}) {
  const [failed, setFailed] = useState(false);

  if (failed) {
    return (
      <div className="wechat-setup-guide-image-placeholder">
        <FileImageOutlined className="wechat-setup-guide-image-placeholder-icon" />
        <Text strong>{placeholderTitle}</Text>
        <Text code>{path}</Text>
      </div>
    );
  }

  return (
    <Image
      src={path}
      alt={alt}
      loading="lazy"
      onError={() => setFailed(true)}
      preview={{ mask: zoomMask }}
    />
  );
}

export default function WeChatSetupGuide() {
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
    <div className="feishu-setup-guide-page wechat-setup-guide-page" ref={pageRef}>
      <header className="feishu-setup-guide-header" ref={headerRef}>
        <div>
          <Button
            type="link"
            icon={<ArrowLeftOutlined />}
            className="feishu-setup-guide-back"
            onClick={() => navigate(CLOUD_DOCUMENTS_WECHAT_OFFICIAL_ACCOUNT_PATH)}
          >
            {t("modelProvider.wechatOfficialAccountSetupGuide.backAccounts")}
          </Button>
          <h1>{t("modelProvider.wechatOfficialAccountSetupGuide.title")}</h1>
          <Paragraph className="feishu-setup-guide-subtitle">
            {t("modelProvider.wechatOfficialAccountSetupGuide.subtitle")}
          </Paragraph>
        </div>
      </header>

      <main className="feishu-setup-guide-shell">
        <aside
          className="feishu-setup-guide-summary"
          aria-label={t("modelProvider.wechatOfficialAccountSetupGuide.summaryAria")}
        >
          <Text strong>{t("modelProvider.wechatOfficialAccountSetupGuide.summaryTitle")}</Text>
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
              id={`wechat-setup-step-${index + 1}`}
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
                    {step.linkLabel ? (
                      <>
                        <a
                          className="feishu-setup-guide-inline-link"
                          href={step.linkHref}
                          target="_blank"
                          rel="noreferrer"
                        >
                          {step.linkLabel}
                        </a>
                        {t("modelProvider.wechatOfficialAccountSetupGuide.linkSeparator")}
                      </>
                    ) : null}
                    {step.description}
                  </Paragraph>
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
              <figure>
                <GuideImage
                  path={step.imagePath}
                  alt={step.alt}
                  zoomMask={t("modelProvider.wechatOfficialAccountSetupGuide.zoomMask")}
                  placeholderTitle={t("modelProvider.wechatOfficialAccountSetupGuide.imagePlaceholder")}
                />
                {step.additionalImage ? (
                  <GuideImage
                    path={step.additionalImage.path}
                    alt={step.additionalImage.alt}
                    zoomMask={t("modelProvider.wechatOfficialAccountSetupGuide.zoomMask")}
                    placeholderTitle={t("modelProvider.wechatOfficialAccountSetupGuide.imagePlaceholder")}
                  />
                ) : null}
              </figure>
            </article>
          ))}
        </section>
      </main>
    </div>
  );
}
