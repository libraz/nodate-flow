/**
 * URL of a path on the flow-web product frontend. The origin comes from
 * `VITE_FLOW_WEB_URL`; unset means the product shares this app's origin, except
 * under the dev server, where flow-web runs on its own port.
 */
export function flowWebUrl(path: string): string {
  const base =
    import.meta.env.VITE_FLOW_WEB_URL ?? (import.meta.env.DEV ? 'http://localhost:5173' : '');
  return `${base.replace(/\/$/, '')}${path}`;
}
