export function connectionTarget(endpoint: string, tokenFile?: string): {socketPath: string; rpcPath: string; url?: never; tokenFile?: never} | {url: string; tokenFile: string; socketPath?: never; rpcPath?: never};
export function authorization(tokenFile: string): {Authorization: string};
export function runtimeEndpoint(config: {endpoint?: string; runtimeDir: string}): string;
