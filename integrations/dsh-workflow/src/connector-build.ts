declare const __LAZYMIND_DSH_BUILD__: string

export const connectorBuild = typeof __LAZYMIND_DSH_BUILD__ === 'string' ? __LAZYMIND_DSH_BUILD__ : 'development'
