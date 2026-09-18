import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { isTrustedCloudNavigation } = require("../electron/src/external-navigation.js");

test("accepts only the configured Cloud origin and login/register paths", () => {
  const origin = "https://cloud.lazymind.example";
  assert.equal(isTrustedCloudNavigation(`${origin}/zh/desktop/authorize?id=fixture`, origin, "login"), true);
  assert.equal(isTrustedCloudNavigation(`${origin}/en/desktop/authorize?id=fixture`, origin, "login"), true);
  assert.equal(isTrustedCloudNavigation(`${origin}/zh/register`, origin, "register"), true);
  assert.equal(isTrustedCloudNavigation(`${origin}/en/register`, origin, "register"), true);
  assert.equal(isTrustedCloudNavigation(`${origin}.attacker.test/zh/desktop/authorize`, origin, "login"), false);
  assert.equal(isTrustedCloudNavigation(`http://cloud.lazymind.example/zh/desktop/authorize`, origin, "login"), false);
  assert.equal(isTrustedCloudNavigation(`${origin}/operator`, origin, "login"), false);
  assert.equal(isTrustedCloudNavigation("javascript:alert(1)", origin, "login"), false);
});

test("allows HTTP only for an explicitly configured local Loopback Cloud", () => {
  for (const origin of ["http://127.0.0.1:8080", "http://localhost:8080"]) {
    assert.equal(isTrustedCloudNavigation(`${origin}/zh/desktop/authorize?id=fixture`, origin, "login"), true);
    assert.equal(isTrustedCloudNavigation(`${origin}/zh/register`, origin, "register"), true);
  }
  assert.equal(isTrustedCloudNavigation("http://192.168.1.8:8080/zh/desktop/authorize", "http://192.168.1.8:8080", "login"), false);
});

test("accepts only the fixed Cloud Console Token Plan destination", () => {
  const origin = "https://cloud.lazymind.example";
  assert.equal(isTrustedCloudNavigation(`${origin}/zh/console#token-plan`, origin, "token-plan"), true);
  assert.equal(isTrustedCloudNavigation(`${origin}/en/console#token-plan`, origin, "token-plan"), true);
  assert.equal(isTrustedCloudNavigation(`${origin}/zh/console#usage`, origin, "token-plan"), false);
  assert.equal(isTrustedCloudNavigation(`${origin}/zh/operator#token-plan`, origin, "token-plan"), false);
  assert.equal(isTrustedCloudNavigation(`${origin}/zh/console?next=https://attacker.test#token-plan`, origin, "token-plan"), false);
  assert.equal(isTrustedCloudNavigation(`${origin}.attacker.test/zh/console#token-plan`, origin, "token-plan"), false);
});
