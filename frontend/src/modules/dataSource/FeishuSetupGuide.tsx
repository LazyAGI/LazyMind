import { useRef } from "react";
import { Button, Image, Typography } from "antd";
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import "./feishuSetupGuide.scss";

const { Paragraph, Text } = Typography;

const FEISHU_OPEN_PLATFORM_URL = "https://open.feishu.cn/app?lang=zh-CN";
const FEISHU_CALLBACK_PATH = "/oauth/feishu/callback";
const FEISHU_FINE_GRAINED_PERMISSIONS = [
  "offline_access",
  "drive:drive",
  "drive:drive:readonly",
  "drive:drive.metadata:readonly",
  "wiki:wiki",
  "wiki:wiki:readonly",
  "wiki:node:retrieve",
  "docx:document",
];

type GuideStep = {
  title: string;
  description: string;
  image: string;
  alt: string;
  details?: string[];
  linkLabel?: string;
  linkHref?: string;
};

function buildGuideSteps(t: TFunction, permissionSeparator: string): GuideStep[] {
  const permissions = FEISHU_FINE_GRAINED_PERMISSIONS.join(permissionSeparator);
  const stepKey = (key: string) => `admin.dataSourceFeishuSetupGuide.steps.${key}`;
  return [
  {
    title: t(stepKey("openPlatformTitle")),
    description: t(stepKey("openPlatformDesc")),
    linkLabel: t("admin.dataSourceFeishuSetupGuide.openPlatform"),
    linkHref: FEISHU_OPEN_PLATFORM_URL,
    image: "/docs/feishu-setup/step-10.png",
    alt: t(stepKey("openPlatformAlt")),
  },
  {
    title: t(stepKey("createAppTitle")),
    description: t(stepKey("createAppDesc")),
    image: "/docs/feishu-setup/step-09.png",
    alt: t(stepKey("createAppAlt")),
  },
  {
    title: t(stepKey("appInfoTitle")),
    description: t(stepKey("appInfoDesc")),
    image: "/docs/feishu-setup/step-08.png",
    alt: t(stepKey("appInfoAlt")),
  },
  {
    title: t(stepKey("permissionsTitle")),
    description: t(stepKey("permissionsDesc")),
    details: [
      t(stepKey("permissionsSimple")),
      t("admin.dataSourceFeishuSetupGuide.fineGrainedPermissions", { permissions }),
    ],
    image: "/docs/feishu-setup/step-07.png",
    alt: t(stepKey("permissionsAlt")),
  },
  {
    title: t(stepKey("publishTitle")),
    description: t(stepKey("publishDesc")),
    image: "/docs/feishu-setup/step-06.png",
    alt: t(stepKey("publishAlt")),
  },
  {
    title: t(stepKey("redirectTitle")),
    description: t(stepKey("redirectDesc")),
    details: [
      t("admin.dataSourceFeishuSetupGuide.callbackFormat", {
        path: FEISHU_CALLBACK_PATH,
      }),
    ],
    image: "/docs/feishu-setup/step-05.png",
    alt: t(stepKey("redirectAlt")),
  },
  {
    title: t(stepKey("credentialsTitle")),
    description: t(stepKey("credentialsDesc")),
    image: "/docs/feishu-setup/step-04.png",
    alt: t(stepKey("credentialsAlt")),
  },
  {
    title: t(stepKey("enterCredentialsTitle")),
    description: t(stepKey("enterCredentialsDesc")),
    image: "/docs/feishu-setup/step-03.png",
    alt: t(stepKey("enterCredentialsAlt")),
  },
  {
    title: t(stepKey("copyFolderTitle")),
    description: t(stepKey("copyFolderDesc")),
    details: [
      t(stepKey("copyFolderDetailTarget")),
      t(stepKey("copyFolderDetailDrive")),
    ],
    image: "/docs/feishu-setup/step-02.png",
    alt: t(stepKey("copyFolderAlt")),
  },
  {
    title: t(stepKey("finishTitle")),
    description: t(stepKey("finishDesc")),
    details: [
      t(stepKey("finishDetail")),
    ],
    image: "/docs/feishu-setup/step-01.png",
    alt: t(stepKey("finishAlt")),
  },
  ];
}

export default function FeishuSetupGuide() {
  const { i18n, t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const pageRef = useRef<HTMLDivElement | null>(null);
  const headerRef = useRef<HTMLElement | null>(null);
  const stepRefs = useRef<Array<HTMLElement | null>>([]);
  const isFromCreateSource =
    new URLSearchParams(location.search).get("from") === "create-source";
  const orderedGuideSteps = buildGuideSteps(
    t,
    i18n.language === "zh-CN" ? "、" : ", ",
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
              navigate(isFromCreateSource ? "/data-sources" : "/data-sources/providers/feishu")
            }
          >
            {isFromCreateSource
              ? t("admin.dataSourceFeishuSetupGuide.backCreateSource")
              : t("admin.dataSourceFeishuSetupGuide.backAccounts")}
          </Button>
          <h1>{t("admin.dataSourceFeishuSetupGuide.title")}</h1>
          <Paragraph className="feishu-setup-guide-subtitle">
            {t("admin.dataSourceFeishuSetupGuide.subtitle")}
          </Paragraph>
        </div>
      </header>

      <main className="feishu-setup-guide-shell">
        <aside
          className="feishu-setup-guide-summary"
          aria-label={t("admin.dataSourceFeishuSetupGuide.summaryAria")}
        >
          <Text strong>{t("admin.dataSourceFeishuSetupGuide.summaryTitle")}</Text>
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
              id={`feishu-setup-step-${index + 1}`}
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
              <figure>
                <Image
                  src={step.image}
                  alt={step.alt}
                  loading="lazy"
                  preview={{
                    mask: t("admin.dataSourceFeishuSetupGuide.zoomMask"),
                  }}
                />
              </figure>
            </article>
          ))}
        </section>
      </main>
    </div>
  );
}
