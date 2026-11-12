import { Component, type ErrorInfo, type ReactNode } from "react";
import i18n from "../../i18n";
import { BASENAME } from "../../globalState";
import "./index.scss";

interface GlobalErrorBoundaryProps {
  children: ReactNode;
}

interface GlobalErrorBoundaryState {
  hasError: boolean;
}

const messages = {
  "zh-CN": {
    eyebrow: "页面遇到了一点问题",
    title: "抱歉，页面暂时无法正常显示",
    description: "可以刷新页面重试，或返回首页继续使用。",
    reload: "刷新页面",
    home: "返回首页",
  },
  "en-US": {
    eyebrow: "Something went wrong",
    title: "Sorry, this page cannot be displayed right now",
    description: "Refresh the page to try again, or return home to continue.",
    reload: "Refresh page",
    home: "Back to home",
  },
} as const;

export function GlobalErrorFallback() {
  const copy = i18n.language === "en-US" ? messages["en-US"] : messages["zh-CN"];

  return (
    <main className="global-error" role="alert" aria-live="assertive">
      <section className="global-error__card" aria-labelledby="global-error-title">
        <div className="global-error__icon" aria-hidden="true">!</div>
        <p className="global-error__eyebrow">{copy.eyebrow}</p>
        <h1 id="global-error-title">{copy.title}</h1>
        <p className="global-error__description">{copy.description}</p>
        <div className="global-error__actions">
          <button type="button" className="global-error__primary" onClick={() => window.location.reload()}>
            {copy.reload}
          </button>
          <button type="button" className="global-error__secondary" onClick={() => window.location.assign(BASENAME || "/")}>
            {copy.home}
          </button>
        </div>
      </section>
    </main>
  );
}

class GlobalErrorBoundary extends Component<
  GlobalErrorBoundaryProps,
  GlobalErrorBoundaryState
> {
  state: GlobalErrorBoundaryState = { hasError: false };

  static getDerivedStateFromError(): GlobalErrorBoundaryState {
    return { hasError: true };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error("[GlobalErrorBoundary] Uncaught render error", error, errorInfo);
  }

  render() {
    return this.state.hasError ? <GlobalErrorFallback /> : this.props.children;
  }
}

export default GlobalErrorBoundary;
