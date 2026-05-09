import { URL, fileURLToPath } from 'node:url';
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
  server: { port: 5173 },
  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          react: ['react', 'react-dom', 'scheduler'],
          tanstack: [
            '@tanstack/react-query',
            '@tanstack/react-router',
            '@tanstack/react-table',
            '@tanstack/react-virtual',
            '@tanstack/router-devtools',
          ],
          sdk: ['@nodate-flow/sdk', 'openapi-fetch'],
          i18n: ['i18next', 'react-i18next', 'i18next-icu', 'intl-messageformat'],
        },
      },
    },
  },
});
