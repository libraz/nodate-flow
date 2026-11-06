/**
 * Vite plugin that bakes the deployment brand into index.html. Loaded natively
 * by Node from the apps' vite.config.ts, so it imports the rules through the
 * package self-reference rather than a relative path.
 */

import { type BrandAsset, resolveBrand } from '@nodate-flow/ui/brand';
import type { HtmlTagDescriptor, Plugin } from 'vite';

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
  let icon: BrandAsset | undefined;
  return {
    name: 'nodate-flow-brand',
    configResolved(config) {
      const brand = resolveBrand(config.env);
      appName = brand.appName;
      icon = brand.icon;
    },
    transformIndexHtml(html) {
      const out = html.replaceAll('%APP_NAME%', () => escapeHtml(appName));
      if (icon === undefined) {
        return out;
      }
      const tags: HtmlTagDescriptor[] = [
        { tag: 'link', attrs: { rel: 'icon', href: icon.src }, injectTo: 'head' },
      ];
      if (icon.darkSrc !== undefined) {
        tags.push({
          tag: 'link',
          attrs: { rel: 'icon', href: icon.darkSrc, media: '(prefers-color-scheme: dark)' },
          injectTo: 'head',
        });
      }
      return { html: out, tags };
    },
  };
}

export default brandPlugin;
