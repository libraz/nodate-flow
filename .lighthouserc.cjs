/** @type {import('@lhci/cli').LighthouseConfig} */
module.exports = {
  ci: {
    collect: {
      // Bind and probe on the same explicit IPv4 loopback. On the Ubuntu
      // runner `localhost` resolves to IPv6 (::1) first, so Vite listening
      // on 127.0.0.1 and headless Chrome navigating to localhost never meet
      // and every run lands on chrome-error://chromewebdata/.
      //
      // NO_COLOR strips the ANSI escapes Vite injects into its ready line
      // (`Local\x1b[22m:`), which otherwise break the readiness regex and
      // make lhci hit the URL before the preview server is listening.
      startServerCommand:
        'cd apps/flow-web && NO_COLOR=1 bun run preview -- --host 127.0.0.1 --port 4173 --strictPort',
      startServerReadyPattern: 'Local:',
      startServerReadyTimeout: 30000,
      // flow-web only serves the product SPA entry here; /login lives in
      // accounts-web, so probing it against this build yields a 404.
      url: ['http://127.0.0.1:4173/'],
      numberOfRuns: 1,
      settings: {
        preset: 'desktop',
        // Only run accessibility and performance categories
        onlyCategories: ['accessibility', 'performance'],
      },
    },
    assert: {
      assertions: {
        'categories:accessibility': ['error', { minScore: 0.95 }],
        'categories:performance': ['warn', { minScore: 0.7 }],
      },
    },
    upload: {
      target: 'temporary-public-storage',
    },
  },
};
