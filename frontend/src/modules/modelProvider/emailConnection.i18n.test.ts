import { describe, expect, it } from "vitest";

import enUS from "@/i18n/locales/en-US";
import zhCN from "@/i18n/locales/zh-CN";

describe("email connection translations", () => {
  it("defines labels used by the connected-account form", () => {
    expect(zhCN.modelProvider.mail.connectedAccounts).toBe("已连接账号");
    expect(zhCN.modelProvider.mail.addAnother).toBe("添加其他账号");
    expect(enUS.modelProvider.mail.connectedAccounts).toBe("Connected accounts");
    expect(enUS.modelProvider.mail.addAnother).toBe("Add another account");
  });
});
