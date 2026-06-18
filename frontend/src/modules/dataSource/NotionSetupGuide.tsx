import { useRef } from "react";
import { Button, Image, Typography } from "antd";
import {
  ArrowLeftOutlined,
  CheckCircleOutlined,
} from "@ant-design/icons";
import { useLocation, useNavigate } from "react-router-dom";
import "./feishuSetupGuide.scss";

const { Paragraph, Text } = Typography;

const NOTION_DEVELOPERS_URL = "https://app.notion.com/developers/connections";
const NOTION_REDIRECT_URI = "http://127.0.0.1:8090/oauth/notion/data-source/callback";

const guideSteps = [
  {
    title: "进入 Notion 开发者网站",
    description:
      "打开 Notion Developers 页面，登录你的 Notion 账号。这里会管理所有 Notion public integration 的 OAuth 凭证。",
    linkLabel: "打开 Notion Developers",
    linkHref: NOTION_DEVELOPERS_URL,
  },
  {
    title: "创建 Public Integration",
    description:
      "在 My connections 页面点击「New integration」创建新的集成应用。注意选择 Public integration 类型（而非 Internal integration），才能支持 OAuth 授权流程。",
    details: [
      "如果已有合适的 Public integration，可以直接使用现有应用。",
      "Integration 名称建议带上 LazyMind 或数据源用途，方便后续识别。",
    ],
  },
  {
    title: "填写 Integration 基本信息",
    description:
      "填写 Integration 名称、描述，并可上传 Logo。这些信息会在用户授权页面展示，建议填写清晰易懂的名称和说明。",
  },
  {
    title: "获取 OAuth Client ID 和 Client Secret",
    description:
      "创建完成后，在 Integration 详情页的「OAuth Domain and URIs」或「Secrets」区域，可以找到 OAuth Client ID 和 Client Secret。这两个凭证将在 LazyMind 中填写。",
    details: [
      "Client ID：Integration 的唯一标识，公开可见。",
      "Client Secret：Integration 的密钥，需要保密，仅在创建时可完整查看。",
    ],
  },
  {
    title: "配置 Redirect URI",
    description:
      "在 Integration 设置的「Redirect URIs」区域，添加 LazyMind 的 OAuth 回调地址。这个地址必须是系统实际使用的回调 URL，否则授权完成后会报错。",
    details: [
      `回调地址：${NOTION_REDIRECT_URI}`,
      "如果是生产环境部署，请将 127.0.0.1 替换为实际的域名或 IP 地址。",
    ],
  },
  {
    title: "配置 Integration 权限 (Capabilities)",
    description:
      "在 Integration 设置中，根据需要勾选以下能力：Read content（读取页面/数据库内容）、Read comments（读取评论）等。LazyMind 至少需要 Read content 权限才能读取 Notion 内容。",
    details: [
      "建议至少勾选 Read content 和 Read user information。",
      "如需写入或更新 Notion 内容，可额外勾选 Insert content 和 Update content。",
    ],
  },
  {
    title: "回到系统填写 Notion 凭据",
    description:
      "回到 LazyMind 数据源管理页面，选择 Notion 数据源类型，弹出凭据配置窗口，填入上一步获取的 Client ID 和 Client Secret，然后保存。",
    details: [
      "Client ID 对应系统弹窗中的 App ID 字段。",
      "Client Secret 对应系统弹窗中的 App Secret 字段。",
    ],
  },
  {
    title: "粘贴 Notion Page/Database 链接并完成授权",
    description:
      "凭据保存后，系统会自动发起 Notion OAuth 授权。授权通过后，在数据源配置中粘贴需要接入的 Notion page 或 database 链接，保存并同步即可。",
    details: [
      "Notion page 链接格式：https://www.notion.so/...",
      "Notion database 链接格式：https://www.notion.so/...",
      "也可以在 Notion 中右键点击页面，选择 Copy link 获取链接。",
      "注意：需要先在 Notion 中将对应页面或数据库连接到该 Integration（在页面右上角 ··· → Connections → 选择你的 Integration）。",
    ],
  },
];

export default function NotionSetupGuide() {
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
              navigate(isFromCreateSource ? "/data-sources" : "/data-sources/providers/notion")
            }
          >
            {isFromCreateSource ? "返回新建数据源" : "返回数据源管理"}
          </Button>
          <h1>数据源管理-新建数据源-Notion</h1>
          <Paragraph className="feishu-setup-guide-subtitle">
            在 Notion Developers 创建 Public Integration，获取 OAuth 凭证并配置 Redirect URI，然后在 LazyMind 中完成 Notion 数据源授权。
          </Paragraph>
        </div>
      </header>

      <main className="feishu-setup-guide-shell">
        <aside className="feishu-setup-guide-summary" aria-label="Notion 接入流程概览">
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
