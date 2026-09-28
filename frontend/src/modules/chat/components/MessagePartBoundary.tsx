import { Component, type ErrorInfo, type ReactNode } from "react";

interface MessagePartBoundaryProps {
  children: ReactNode;
  fallback: ReactNode;
}

interface MessagePartBoundaryState {
  failed: boolean;
}

export default class MessagePartBoundary extends Component<
  MessagePartBoundaryProps,
  MessagePartBoundaryState
> {
  state: MessagePartBoundaryState = { failed: false };

  static getDerivedStateFromError(): MessagePartBoundaryState {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("[MessagePartBoundary] Uncaught render error", error, info);
  }

  render() {
    if (this.state.failed) {
      return this.props.fallback;
    }
    return this.props.children;
  }
}
