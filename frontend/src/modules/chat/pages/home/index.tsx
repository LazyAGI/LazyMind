import { useEffect } from "react";
import NewChatPage from "../newChat";
import "./index.scss";

function Home() {
  useEffect(() => {
    window.lazymindDesktop?.notifyAppReady?.();
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
