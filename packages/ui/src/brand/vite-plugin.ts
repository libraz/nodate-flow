/**
 * Vite plugin that bakes the deployment brand into index.html. Loaded natively
 * by Node from the apps' vite.config.ts, so it imports the rules through the
 * package self-reference rather than a relative path.
 */

import { resolveBrand } from '@nodate-flow/ui/brand';
import type { Plugin } from 'vite';

const HTML_ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => HTML_ESCAPES[c] ?? c);
}

export function brandPlugin(): Plugin {
  let appName = '';
  let logoUrl: string | undefined;
  return {
    name: 'nodate-flow-brand',
    configResolved(config) {
      const brand = resolveBrand(config.env);
      appName = brand.appName;
      logoUrl = brand.logoUrl;
    },
    transformIndexHtml(html) {
      const out = html.replaceAll('%APP_NAME%', () => escapeHtml(appName));
      if (logoUrl === undefined) {
        return out;
      }
      return {
        html: out,
        tags: [{ tag: 'link', attrs: { rel: 'icon', href: logoUrl }, injectTo: 'head' }],
      };
    },
  };
}

export default brandPlugin;
