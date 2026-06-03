import { useRef } from "react";
import { Button, Typography } from "antd";
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
} from "@ant-design/icons";
import { useLocation, useNavigate } from "react-router-dom";
import "./feishuSetupGuide.scss";

const { Paragraph, Text } = Typography;

const FEISHU_OPEN_PLATFORM_URL = "https://open.feishu.cn/app?lang=zh-CN";

const guideSteps = [
  {
    title: "进入飞书开发平台",
    description:
      "打开飞书开发平台，在企业自建应用页面点击开发者后台，为 LazyRAG 准备数据源授权应用。",
    image: "/docs/feishu-setup/step-01.jpg",
    alt: "飞书开发平台首页与开发者后台入口",
  },
  {
    title: "创建企业自建应用",
    description:
      "在开发者后台中选择创建企业自建应用，用于后续配置 App ID、App Secret 与回调地址。",
    image: "/docs/feishu-setup/step-02.jpg",
    alt: "飞书开发者后台创建企业自建应用入口",
  },
  {
    title: "填写应用名称和描述",
    description:
      "填写应用名称与应用描述，建议使用能识别业务用途的名称，方便后续在数据源授权列表中管理。",
    image: "/docs/feishu-setup/step-03.jpg",
    alt: "飞书企业自建应用名称和描述表单",
  },
  {
    title: "配置应用权限",
    description:
      "进入权限管理，按数据源同步需要开启文档、知识库或云空间相关权限，保存后进入发布流程。",
    image: "/docs/feishu-setup/step-04.jpg",
    alt: "飞书开放平台权限管理页面",
  },
  {
    title: "发布应用版本",
    description:
      "提交发布申请并确认发布，让新配置的权限和应用信息生效。",
    image: "/docs/feishu-setup/step-05.jpg",
    alt: "飞书开放平台确认提交发布申请弹窗",
  },
  {
    title: "配置重定向 URL",
    description:
      "进入安全设置，在重定向 URL 中填写 LazyRAG 飞书 OAuth 回调地址，完成后即可回到系统创建飞书账号并授权。",
    image: "/docs/feishu-setup/step-06.jpg",
    alt: "飞书开放平台安全设置重定向 URL 配置页面",
  },
];

export default function FeishuSetupGuide() {
  const navigate = useNavigate();
  const location = useLocation();
  const pageRef = useRef<HTMLDivElement | null>(null);
  const stepRefs = useRef<Array<HTMLElement | null>>([]);
  const isFromCreateSource =
    new URLSearchParams(location.search).get("from") === "create-source";

  const scrollToStep = (index: number) => {
    const page = pageRef.current;
    const target = stepRefs.current[index];

    if (!page || !target) {
      return;
    }

    const pageRect = page.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();

    page.scrollTo({
      top: page.scrollTop + targetRect.top - pageRect.top - 12,
      behavior: "smooth",
    });
  };

  return (
    <div className="feishu-setup-guide-page" ref={pageRef}>
      <header className="feishu-setup-guide-header">
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
            从飞书开发平台创建企业自建应用，并在 LazyRAG 中完成飞书数据源授权。
          </Paragraph>
        </div>
      </header>

      <main className="feishu-setup-guide-shell">
        <aside className="feishu-setup-guide-summary" aria-label="飞书接入流程概览">
          <Text strong>准备流程</Text>
          <ol>
            {guideSteps.map((step, index) => (
              <li key={step.title}>
                <button type="button" onClick={() => scrollToStep(index)}>
                  {step.title}
                </button>
              </li>
            ))}
          </ol>
          <a href={FEISHU_OPEN_PLATFORM_URL} target="_blank" rel="noreferrer">
            打开飞书开发平台
          </a>
        </aside>

        <section className="feishu-setup-guide-content">
          {guideSteps.map((step, index) => (
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
                  <Paragraph>{step.description}</Paragraph>
                </div>
                <CheckCircleOutlined className="feishu-setup-guide-step-icon" />
              </div>
              <figure>
                <img src={step.image} alt={step.alt} loading="lazy" />
              </figure>
            </article>
          ))}
        </section>
      </main>
    </div>
  );
}
