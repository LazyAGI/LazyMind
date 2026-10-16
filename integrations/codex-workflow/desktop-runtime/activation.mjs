import fs from 'node:fs';
import { atomicJSON } from './config.mjs';

// Keep the registration discoverable until every restoration step has finished.
// A failed unregister can be retried even after activation.json was removed.
export function disableActivation(config, stateFile, state, ops) {
  if (state) {
    if (ops.running(config)) throw new Error('Quit Codex normally before disconnecting');
    ops.validate(state);
    atomicJSON(stateFile, { ...state, disabling: true });
    ops.stop?.();
    ops.set(state.before);
    ops.restore();
    ops.remove?.();
    fs.unlinkSync(stateFile);
  }
  ops.unregister(config);
}
