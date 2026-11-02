const path = require('node:path');
const { spawnSync } = require('node:child_process');

function codesign(args) {
  const result = spawnSync('/usr/bin/codesign', args, { encoding: 'utf8' });
  if (result.error) throw result.error;
  const output = `${result.stdout || ''}\n${result.stderr || ''}`.trim();
  if (result.status !== 0) throw new Error(`Privacy identity signature check failed: ${output}`);
  return output;
}

function verifyMacosPrivacyIdentity(appPath, { expectedTeamId = '', run = codesign } = {}) {
  if (expectedTeamId && !/^[A-Z0-9]{10}$/.test(expectedTeamId)) {
    throw new Error('Expected macOS signing Team ID must contain 10 uppercase letters or digits');
  }
  const targets = [
    [appPath, 'ai.lazymind.desktop'],
    [path.join(appPath, 'Contents/Resources/recording-helper/LazyMind Recorder.app'), 'ai.lazymind.recorder'],
  ];
  const identities = targets.map(([target, identifier]) => {
    run(['--verify', '--strict', target]);
    const signature = run(['--display', '--verbose=4', target]);
    const team = signature.match(/^TeamIdentifier=([A-Z0-9]{10})$/m)?.[1];
    if (!team || !/^Authority=Developer ID Application:/m.test(signature)) {
      throw new Error(`${identifier} requires Developer ID signing; ad-hoc builds cannot preserve privacy identity across rebuilds`);
    }
    if (!signature.split(/\r?\n/).includes(`Identifier=${identifier}`)) {
      throw new Error(`Unexpected privacy-sensitive bundle identifier; expected ${identifier}`);
    }
    const requirement = run(['--display', '--requirements', '-', target]);
    if (!/^designated => /m.test(requirement) || /\bcdhash\b/i.test(requirement)) {
      throw new Error(`${identifier} has a missing or binary-hash-bound designated requirement`);
    }
    if (!/\banchor apple generic\b/.test(requirement)
        || !requirement.includes(`identifier "${identifier}"`)
        || !new RegExp(`certificate leaf\\[subject\\.OU\\] = (?:"${team}"|${team})(?:\\s|$|\\))`).test(requirement)) {
      throw new Error(`${identifier} must retain its Apple Team and bundle identity in the designated requirement`);
    }
    const requiredTeam = expectedTeamId || team;
    // Verify the certificate chain and Team rather than trusting display text.
    run(['--verify', '--strict', '--test-requirement',
      `=identifier "${identifier}" and anchor apple generic and certificate leaf[subject.OU] = "${requiredTeam}"`, target]);
    if (team !== requiredTeam) throw new Error(`${identifier} is signed by an unexpected Apple Team`);
    return { identifier, team, designatedRequirement: requirement.match(/^designated => (.+)$/m)[1] };
  });
  if (identities[0].team !== identities[1].team) {
    throw new Error('Desktop and Recorder must use the same Apple signing Team');
  }
  return { schemaVersion: 1, identities };
}

module.exports = { verifyMacosPrivacyIdentity };

if (require.main === module) {
  const [appPath, expectedTeamId = process.env.LAZYMIND_MACOS_TEAM_ID || ''] = process.argv.slice(2);
  if (!appPath) throw new Error('Usage: verify-macos-privacy-identity.cjs /path/to/LazyMind.app [TEAM_ID]');
  console.log(JSON.stringify(verifyMacosPrivacyIdentity(appPath, { expectedTeamId }), null, 2));
}
