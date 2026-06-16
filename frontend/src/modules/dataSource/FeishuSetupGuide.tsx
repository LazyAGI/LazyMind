import { useRef } from "react";
import { Button, Image, Typography } from "antd";
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
} from "@ant-design/icons";
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

const guideSteps = [
  {
    title: "进入飞书开发平台",
    description:
      "打开飞书开放平台后，点击右上角的开发者后台，进入企业自建应用管理页面。这里会创建一个专门给 LazyMind 使用的飞书授权应用。",
    linkLabel: "打开飞书开发平台",
    linkHref: FEISHU_OPEN_PLATFORM_URL,
    image: "/docs/feishu-setup/step-10.png",
    alt: "飞书开发平台首页与开发者后台入口",
  },
  {
    title: "创建企业自建应用",
    description:
      "进入开发者后台后，点击创建企业自建应用，准备创建一个专门用于飞书数据源接入的应用。",
    image: "/docs/feishu-setup/step-09.png",
    alt: "飞书开发者后台创建企业自建应用入口",
  },
  {
    title: "填写应用名称和描述",
    description:
      "填写应用名称和应用描述并完成创建。建议名称里带上 LazyMind 或数据源用途，后续在飞书后台和 LazyMind 中都更容易识别。",
    image: "/docs/feishu-setup/step-08.png",
    alt: "飞书企业自建应用名称和描述表单",
  },
  {
    title: "开通所需权限",
    description:
      "创建应用后进入权限管理页面，点击开通权限，搜索并添加 LazyMind 访问飞书数据源所需的权限。注意，如果想访问个人名下的知识，一定要配置用户身份权限(user_access_token)，而不是应用身份权限。",
    details: [
      "通用版本：添加 offline_access、drive、wiki、docx，并勾选对应权限即可。",
      `细致版本：${FEISHU_FINE_GRAINED_PERMISSIONS.join("、")}`,
    ],
    image: "/docs/feishu-setup/step-07.png",
    alt: "飞书开放平台权限管理页面与开通权限入口",
  },
  {
    title: "发布应用版本",
    description:
      "完成权限配置后，进入版本管理与发布，提交新版本并确认发布。发布成功后，新权限才会正式生效。",
    image: "/docs/feishu-setup/step-06.png",
    alt: "飞书开放平台确认发布应用版本弹窗",
  },
  {
    title: "配置重定向 URL",
    description:
      "进入安全设置，将系统的 OAuth 回调地址加入重定向 URL。这个地址必须和系统发起授权时使用的回调地址一致。",
    details: [
      `回调地址格式：http://前端应用的 IP 和端口${FEISHU_CALLBACK_PATH}`,
    ],
    image: "/docs/feishu-setup/step-05.png",
    alt: "飞书开放平台安全设置中的重定向 URL 配置页面",
  },
  {
    title: "复制 App ID 与 App Secret",
    description:
      "回到应用的凭证与基础信息页面，复制 App ID 和 App Secret，准备粘贴到系统里。",
    image: "/docs/feishu-setup/step-04.png",
    alt: "飞书开放平台应用凭证页面中的 App ID 与 App Secret",
  },
  {
    title: "回到系统填写飞书凭据",
    description:
      "回到数据源管理，打开飞书 App 凭据弹窗，填入刚刚复制的 App ID 和 App Secret，并保存。",
    image: "/docs/feishu-setup/step-03.png",
    alt: "系统内填写飞书 App ID 与 App Secret 的弹窗",
  },
  {
    title: "复制飞书文件夹 ID",
    description:
      "在飞书云文档中打开目标文件夹，复制浏览器地址栏中的文件夹 ID；如果接入 Wiki，则复制对应的 Wiki 空间 ID。",
    details: [
      "目标类型可根据你的接入目标选择 Drive 文件夹或 Wiki。",
      "Drive 文件夹场景下，可直接从浏览器地址栏复制文件夹 ID。",
    ],
    image: "/docs/feishu-setup/step-02.png",
    alt: "飞书云文档文件夹地址栏中的文件夹 ID",
  },
  {
    title: "填写文件夹 ID 并完成授权",
    description:
      "回到数据源创建弹窗，选择目标类型，填入上一步复制的 ID，然后点击连接账号完成 OAuth 授权并保存配置。",
    details: [
      "授权前请先在飞书云盘中创建文件夹，并把文件夹目录地址复制到 LazyMind。",
    ],
    image: "/docs/feishu-setup/step-01.png",
    alt: "系统内填写飞书文件夹 ID 并发起授权",
  },
];

export default function FeishuSetupGuide() {
  const navigate = useNavigate();
  const location = useLocation();
  const pageRef = useRef<HTMLDivElement | null>(null);
  const headerRef = useRef<HTMLElement | null>(null);
  const stepRefs = useRef<Array<HTMLElement | null>>([]);
  const isFromCreateSource =
    new URLSearchParams(location.search).get("from") === "create-source";
  const orderedGuideSteps = guideSteps;

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
            {isFromCreateSource ? "返回新建数据源" : "返回飞书账号"}
          </Button>
          <h1>数据源管理-新建数据源-飞书</h1>
          <Paragraph className="feishu-setup-guide-subtitle">
            从飞书开发平台创建企业自建应用，并在 LazyMind 中完成飞书数据源授权。
          </Paragraph>
        </div>
      </header>

      <main className="feishu-setup-guide-shell">
        <aside className="feishu-setup-guide-summary" aria-label="飞书接入流程概览">
          <Text strong>准备流程</Text>
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
                    mask: "点击放大查看",
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
