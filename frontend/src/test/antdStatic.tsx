import { act, cleanup, waitFor } from "@testing-library/react";
import { ConfigProvider, Modal, message } from "antd";
import { afterAll, afterEach, beforeAll, expect } from "vitest";

// Static Ant Design APIs mount outside Testing Library's render roots.
export function setupAntdStaticCleanup() {
  beforeAll(() => {
    ConfigProvider.config({
      holderRender: (children) => <ConfigProvider theme={{ token: { motion: false } }}>{children}</ConfigProvider>,
    });
  });

  afterEach(async () => {
    cleanup();
    await act(async () => { Modal.destroyAll(); message.destroy(); });
    await waitFor(() => {
      expect(document.querySelector(".ant-modal-root, .ant-message-notice")).toBeNull();
    });
  });

  afterAll(() => { ConfigProvider.config({ holderRender: undefined }); });
}
