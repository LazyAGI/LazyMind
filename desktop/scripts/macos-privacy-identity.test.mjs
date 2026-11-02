import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import path from 'node:path';
const { verifyMacosPrivacyIdentity } = createRequire(import.meta.url)('./verify-macos-privacy-identity.cjs');

const team = 'ABCDE12345';
const app = '/fixture/LazyMind.app';
const helper = path.join(app, 'Contents/Resources/recording-helper/LazyMind Recorder.app');
function signed(identifier, overrides = {}) {
  return {
    identifier, team, authority: 'Developer ID Application: LazyMind',
    requirement: `designated => identifier "${identifier}" and anchor apple generic and certificate leaf[subject.OU] = ${team}`,
    ...overrides,
  };
}
function fixture(helperOverrides = {}, appOverrides = {}) {
  const targets = new Map([[app, signed('ai.lazymind.desktop', appOverrides)],
    [helper, signed('ai.lazymind.recorder', helperOverrides)]]);
  const calls = [];
  const run = (args) => {
    calls.push(args);
    const signature = targets.get(args.at(-1));
    if (args.includes('--test-requirement')) {
      assert.ok(args.includes('--strict'));
      assert.ok(args.some(value => value.includes('anchor apple generic') && value.includes(`subject.OU] = "${signature.team}"`)));
    }
    if (args.includes('--verify')) return '';
    if (args.includes('--requirements')) return signature.requirement;
    return `Identifier=${signature.identifier}\nTeamIdentifier=${signature.team}\nAuthority=${signature.authority}\n`;
  };
  return { run, calls };
}

test('validates both final application signatures against the expected release Team', () => {
  const { run, calls } = fixture();
  const report = verifyMacosPrivacyIdentity(app, { expectedTeamId: team, run });
  assert.deepEqual(report.identities.map(row => row.identifier), ['ai.lazymind.desktop', 'ai.lazymind.recorder']);
  assert.equal(calls.filter(args => args.includes('--test-requirement')).length, 2);
  assert.ok(calls.some(args => args.includes('--verify') && args.at(-1) === helper));
});

test('local Developer ID packages can derive their shared Team from the signed binaries', () => {
  const { run } = fixture();
  assert.equal(verifyMacosPrivacyIdentity(app, { run }).identities[1].team, team);
});

for (const [label, overrides, pattern] of [
  ['ad-hoc recorder', { team: 'not set', authority: '' }, /Developer ID/],
  ['development certificate', { authority: 'Apple Development: LazyMind' }, /Developer ID/],
  ['changed bundle identifier', { identifier: 'ai.lazymind.recorder.v2' }, /bundle identifier/],
  ['changed signing Team', { team: 'OTHER12345' }, /Apple Team/],
  ['binary hash identity', { requirement: 'designated => cdhash H"1234"' }, /binary-hash-bound/],
  ['missing identity', { requirement: '' }, /missing/],
  ['unbound identity', { requirement: 'designated => identifier "ai.lazymind.recorder"' }, /retain its Apple Team/],
]) test(`blocks release with ${label}`, () => {
  assert.throws(() => verifyMacosPrivacyIdentity(app, { expectedTeamId: team, ...fixture(overrides) }), pattern);
});

test('rejects mismatched app and helper Teams even without an explicit release Team', () => {
  const overrides = signed('ai.lazymind.recorder', {
    team: 'OTHER12345',
    requirement: 'designated => identifier "ai.lazymind.recorder" and anchor apple generic and certificate leaf[subject.OU] = OTHER12345',
  });
  const { run } = fixture(overrides);
  assert.throws(() => verifyMacosPrivacyIdentity(app, { run }), /same Apple signing Team/);
});

test('propagates invalid signatures and missing packaged Recorder failures', () => {
  assert.throws(() => verifyMacosPrivacyIdentity(app, {
    run: () => { throw new Error('invalid signature or missing helper'); },
  }), /invalid signature or missing helper/);
});

test('rejects an invalid configured Team before invoking codesign', () => {
  assert.throws(() => verifyMacosPrivacyIdentity(app, {
    expectedTeamId: 'bad', run: () => assert.fail('must not run'),
  }), /Team ID/);
});
