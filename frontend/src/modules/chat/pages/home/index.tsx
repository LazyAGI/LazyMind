import { useEffect } from "react";
import NewChatPage from "../newChat";
import "./index.scss";

function Home() {
  useEffect(() => {
    let secondFrame: number | undefined;
    const firstFrame = window.requestAnimationFrame(() => {
      secondFrame = window.requestAnimationFrame(() => {
        window.lazymindDesktop?.notifyAppReady?.();
      });
    });
    return () => {
      window.cancelAnimationFrame(firstFrame);
      if (secondFrame !== undefined) {
        window.cancelAnimationFrame(secondFrame);
      }
    };
  }, []);

  return (
    <div className="chat-wrapper">
      <div className="chat-content">
        <NewChatPage />
      </div>
    </div>
  );
}

export default Home;
