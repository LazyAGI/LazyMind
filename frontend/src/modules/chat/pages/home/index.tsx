import { useEffect } from "react";
import ConversationContent from './ConversationContent';
import "./index.scss";

function Home() {
  useEffect(() => {
    window.lazymindDesktop?.notifyAppReady?.();
  }, []);

  return (
    <div className="chat-wrapper">
      <div className="chat-content">
        <ConversationContent />
      </div>
    </div>
  );
}

export default Home;
