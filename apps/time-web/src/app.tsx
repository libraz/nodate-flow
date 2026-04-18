import { RouterProvider } from '@tanstack/react-router';
import type { ReactElement } from 'react';

import { QueryProvider } from './providers/query-provider';
import { router } from './router/router';

export default function App(): ReactElement {
  return (
    <QueryProvider>
      <RouterProvider router={router} />
    </QueryProvider>
  );
}
