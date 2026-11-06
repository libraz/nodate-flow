// @vitest-environment node
import type { ResolvedConfig } from 'vite';
import { describe, expect, it } from 'vitest';
import { brandPlugin } from './vite-plugin';

type Hooks = {
  configResolved: (c: ResolvedConfig) => void;
  transformIndexHtml: (html: string) => string | { html: string; tags: unknown[] };
};

function setup(env: Record<string, unknown>): Hooks {
  // Called as plain functions: the hooks under test do not use Rollup's `this`.
  const plugin = brandPlugin() as unknown as Hooks;
  plugin.configResolved({ env } as unknown as ResolvedConfig);
  return plugin;
}

describe('brandPlugin', () => {
  it('replaces %APP_NAME% with the default and adds no icon when unset', () => {
    const out = setup({}).transformIndexHtml('<title>%APP_NAME% - Account</title>');
    expect(out).toBe('<title>nodate-flow - Account</title>');
  });

  it('escapes HTML metacharacters and keeps $ patterns literal', () => {
    const out = setup({ VITE_APP_NAME: `<A>&"'$&` }).transformIndexHtml(
      '<title>%APP_NAME%</title>',
    );
    expect(out).toBe('<title>&lt;A&gt;&amp;&quot;&#39;$&amp;</title>');
  });

  it('adds an icon link only when a logo is set', () => {
    const out = setup({ VITE_APP_LOGO_URL: 'https://cdn.example.com/logo.svg' }).transformIndexHtml(
      '<title>%APP_NAME%</title>',
    );
    expect(out).toEqual({
      html: '<title>nodate-flow</title>',
      tags: [
        {
          tag: 'link',
          attrs: { rel: 'icon', href: 'https://cdn.example.com/logo.svg' },
          injectTo: 'head',
        },
      ],
    });
  });

  it('throws from configResolved on an invalid value', () => {
    expect(() => setup({ VITE_APP_LOGO_URL: 'http://x.example/l.png' })).toThrow(
      /VITE_APP_LOGO_URL/,
    );
    expect(() => setup({ VITE_APP_NAME: 'a:b' })).toThrow(/VITE_APP_NAME/);
  });
});
