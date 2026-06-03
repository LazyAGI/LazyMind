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
const FEISHU_CALLBACK_PATH = "/oauth/feishu/data-source/callback";
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
      "进入企业自建应用页面后点击开发者后台。这里会创建一个专门给 LazyRAG 使用的飞书授权应用。",
    linkLabel: "打开飞书开发平台",
    linkHref: FEISHU_OPEN_PLATFORM_URL,
    image: "/docs/feishu-setup/step-01.jpg",
    alt: "飞书开发平台首页与开发者后台入口",
  },
  {
    title: "创建企业自建应用",
    description:
      "在开发者后台点击创建企业自建应用。这个应用后续会提供 App ID、App Secret 和回调地址配置，用来连接飞书云盘数据。",
    image: "/docs/feishu-setup/step-02.jpg",
    alt: "飞书开发者后台创建企业自建应用入口",
  },
  {
    title: "填写应用名称和描述",
    description:
      "填写应用名称和应用描述。建议名称里带上 LazyRAG 或数据源用途，后续在飞书后台和 LazyRAG 中都更容易识别。",
    image: "/docs/feishu-setup/step-03.jpg",
    alt: "飞书企业自建应用名称和描述表单",
  },
  {
    title: "配置应用权限",
    description:
      "创建完成后进入权限管理，再打开开通权限。通用配置可以搜索并添加 offline_access、drive、wiki、docx 相关权限；如果需要逐项配置，可按下方清单添加。",
    details: [
      "通用版本：添加 offline_access、drive、wiki、docx，并勾选对应权限即可。",
      `细致版本：${FEISHU_FINE_GRAINED_PERMISSIONS.join("、")}`,
    ],
    image: "/docs/feishu-setup/step-04.jpg",
    alt: "飞书开放平台权限管理页面",
  },
  {
    title: "发布应用版本",
    description:
      "权限添加完成后，进入版本管理与发布，创建一个新版本，填写版本信息并确认发布。发布成功后，刚才配置的权限才会正式生效。",
    image: "/docs/feishu-setup/step-05.jpg",
    alt: "飞书开放平台确认提交发布申请弹窗",
  },
  {
    title: "配置重定向 URL 并回到 LazyRAG 授权",
    description:
      "应用发布后进入安全设置，把 LazyRAG 的飞书 OAuth 回调地址添加到重定向 URL。随后复制 App ID 和 App Secret 到 LazyRAG，保存后选择飞书云盘文件夹，点击连接账号完成授权。",
    details: [
      `回调地址格式：http://前端应用的 IP 和端口${FEISHU_CALLBACK_PATH}`,
      "授权前请先在飞书云盘中创建文件夹，并把文件夹目录地址复制到 LazyRAG。",
    ],
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
                <img src={step.image} alt={step.alt} loading="lazy" />
              </figure>
            </article>
          ))}
        </section>
      </main>
    </div>
  );
}
