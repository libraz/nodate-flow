/** @type {import('@lhci/cli').LighthouseConfig} */
module.exports = {
  ci: {
    collect: {
      // NO_COLOR strips the ANSI escapes Vite injects into its ready line
      // (`Local\x1b[22m:`), which otherwise break the readiness regex and
      // make lhci hit the URL before the preview server is listening.
      startServerCommand: 'cd apps/flow-web && NO_COLOR=1 bun run preview',
      startServerReadyPattern: 'Local:',
      startServerReadyTimeout: 30000,
      url: ['http://localhost:4173/', 'http://localhost:4173/login'],
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
