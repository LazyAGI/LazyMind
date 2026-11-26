import type { DesktopAgent, DesktopAgentBindingTarget } from "@/runtime/desktopBridge";

export interface AgentDefinition {
  id: DesktopAgent;
  name: string;
  /** Label for sessions this integration can import into chat history. */
  conversationSourceLabel?: string;
  icon: string;
  installURL: string;
  executorInstallURL?: string;
  executorName?: string;
  executorLoginMode?: "automatic" | "interactive";
  executorLoginURL?: string;
  mcpBindingTarget?: DesktopAgentBindingTarget;
  executorBindingTarget?: DesktopAgentBindingTarget;
}

export const AGENTS: AgentDefinition[] = [
  {
    id: "codex", name: "Codex", icon: "/assistant-icons/codex.png",
    conversationSourceLabel: "Codex CLI",
    installURL: "https://learn.chatgpt.com/docs/app",
    executorName: "Codex CLI", executorLoginMode: "automatic",
    mcpBindingTarget: "codex-desktop", executorBindingTarget: "codex-cli",
  },
  {
    id: "cursor", name: "Cursor", icon: "/assistant-icons/cursor.png",
    conversationSourceLabel: "Cursor Agent CLI",
    installURL: "https://cursor.com/downloads",
    executorInstallURL: "https://cursor.com/docs/cli/installation",
    executorName: "Cursor Agent CLI", executorLoginMode: "automatic",
    mcpBindingTarget: "cursor-desktop", executorBindingTarget: "cursor-cli",
  },
  {
    id: "workbuddy", name: "WorkBuddy", icon: "/assistant-icons/workbuddy.svg",
    conversationSourceLabel: "WorkBuddy",
    installURL: "https://www.workbuddy.cn",
    executorName: "WorkBuddy CLI", executorLoginMode: "automatic",
    mcpBindingTarget: "workbuddy-desktop",
  },
  {
    id: "raccoon", name: "Raccoon", icon: "/assistant-icons/raccoon.svg",
    installURL: "https://office.xiaohuanxiong.com/download",
    mcpBindingTarget: "raccoon-desktop",
  },
  {
    id: "traework", name: "TRAE Work", icon: "/assistant-icons/traework.png",
    installURL: "https://www.trae.ai",
    mcpBindingTarget: "traework-desktop",
  },
  {
    id: "deepseek-harness", name: "DeepSeek Harness", icon: "/assistant-icons/deepseek.png",
    conversationSourceLabel: "DeepSeek Harness (DSH)",
    executorName: "DeepSeek Harness",
    installURL: "https://github.com/deepseek-ai/deepseek-harness",
  },
];

export function agentIntegrationHref(id: string): string {
  return `/settings?section=assistants&agent=${encodeURIComponent(id)}`;
}
