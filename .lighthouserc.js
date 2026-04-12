/** @type {import('@lhci/cli').LighthouseConfig} */
export default {
  ci: {
    collect: {
      startServerCommand: 'cd apps/web && bun run preview',
      startServerReadyPattern: 'Local:',
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
