const assert = require("node:assert/strict");
const test = require("node:test");

const { isTrustedFeishuCLINavigation } = require("./external-navigation");

test("Feishu CLI navigation accepts only exact official routes", () => {
  for (const url of [
    "https://open.feishu.cn/page/cli?user_code=fixture&from=cli",
    "https://open.larksuite.com/page/cli?user_code=fixture&from=cli",
    "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=fixture",
    "https://accounts.larksuite.com/oauth/v1/device/verify?flow_id=fixture",
  ]) {
    assert.equal(isTrustedFeishuCLINavigation(url), true, url);
  }
  for (const url of [
    "https://open.feishu.cn/app",
    "https://accounts.feishu.cn/page/cli",
    "https://accounts.feishu.cn:444/oauth/v1/device/verify",
    "https://accounts.feishu.cn.example.invalid/oauth/v1/device/verify",
  ]) {
    assert.equal(isTrustedFeishuCLINavigation(url), false, url);
  }
});
