import { fileURLToPath, URL } from 'node:url';
import tailwindcss from '@tailwindcss/vite';
import { TanStackRouterVite } from '@tanstack/router-plugin/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [
    TanStackRouterVite({
      routesDirectory: './src/routes',
      generatedRouteTree: './src/routeTree.gen.ts',
      routeFileIgnorePattern: '\\.test\\.[jt]sx?$',
      quoteStyle: 'single',
      semicolons: true,
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: { port: 5175 },
  build: {
    rollupOptions: {
      output: {
        // rolldown-vite replaces rollup's `output.manualChunks` with
        // `output.advancedChunks.groups`. Each group matches resolved
        // module ids (paths) via a `test` regex. Trailing `[\\/]` keeps
        // package-name alternations from over-matching (e.g. `react`
        // must not swallow `react-i18next`).
        advancedChunks: {
          groups: [
            {
              name: 'react',
              test: /[\\/]node_modules[\\/](?:react|react-dom|scheduler)[\\/]/,
            },
            {
              name: 'tanstack',
              test: /[\\/]node_modules[\\/]@tanstack[\\/](?:react-query|react-router|router-devtools)[\\/]/,
            },
            {
              name: 'i18n',
              test: /[\\/]node_modules[\\/](?:i18next|react-i18next|i18next-icu|intl-messageformat)[\\/]/,
            },
          ],
        },
      },
    },
  },
});
