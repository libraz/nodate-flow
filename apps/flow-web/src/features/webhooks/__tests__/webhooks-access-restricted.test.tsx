/** A 401/403 on the webhook list shows the access-restricted panel, not the generic load error. */

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../../../lib/api-error';

const mocks = vi.hoisted(() => ({ error: null as unknown }));

vi.mock('@tanstack/react-router', () => ({
  getRouteApi: () => ({ useParams: () => ({ id: 'ws-1' }) }),
}));

vi.mock('../../workspaces/access-restricted', () => ({
  default: ({ sectionTitleKey }: { sectionTitleKey: string }) => (
    <div data-testid="access-restricted">{sectionTitleKey}</div>
  ),
}));

vi.mock('../../../components/page-header', () => ({
  default: ({ title }: { title: string }) => <h1>{title}</h1>,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { resolvedLanguage: 'en' } }),
}));

const idleMutation = { isPending: false, mutateAsync: vi.fn(), variables: undefined };

vi.mock('../api', () => ({
  useWebhooksQuery: () => ({
    data: undefined,
    error: mocks.error,
    isPending: false,
    isError: true,
    isFetching: false,
    refetch: vi.fn(),
  }),
  useWebhookQuery: () => ({ data: undefined, isPending: false }),
  useDeliveriesQuery: () => ({ data: undefined, isPending: false }),
  useCreateWebhookMutation: () => idleMutation,
  useDeleteWebhookMutation: () => idleMutation,
  useToggleWebhookMutation: () => idleMutation,
  useTestWebhookMutation: () => idleMutation,
}));

import WebhooksSettingsPage from '../webhooks-settings-page';

beforeEach(() => {
  mocks.error = null;
});

describe('WebhooksSettingsPage access', () => {
  it.each([401, 403])('renders AccessRestricted for a %i', (status) => {
    mocks.error = new ApiError('WS.FORBIDDEN', 'forbidden', status);
    render(<WebhooksSettingsPage />);
    expect(screen.getByTestId('access-restricted').textContent).toBe('nav.webhooks');
    expect(screen.queryByText('settings.webhooks.load_error')).toBeNull();
  });

  it('keeps the generic load error for other failures', () => {
    mocks.error = new ApiError('WS.INTERNAL', 'boom', 500);
    render(<WebhooksSettingsPage />);
    expect(screen.queryByTestId('access-restricted')).toBeNull();
    expect(screen.getByText('settings.webhooks.load_error')).toBeTruthy();
  });
});
