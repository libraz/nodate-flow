/**
 * NotificationsForm writes to the preferences endpoint.
 *
 * The screen this replaces posted five booleans to `PATCH /me` that no
 * delivery path ever read: every save returned 200 and changed nothing.
 * Asserting "the form submits" would reproduce that, so the assertions
 * here are about *where* it submits and *what* it sends — the request
 * has to reach `PUT /workspaces/{wsId}/notification-preferences` with
 * the toggled category carried as a mute.
 *
 * The switch reads inverted on purpose: it is labelled by what the user
 * receives, while the stored field is a mute. Turning a category off
 * therefore has to arrive as `muted: true`.
 */

import type { components } from '@nodate-flow/sdk';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import { type ReactElement, type ReactNode, Suspense } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type Preference = components['schemas']['NotificationPreferenceDTO'];

interface PutCall {
  path: string;
  wsId: string | undefined;
  preferences: Preference[];
}

const sdkMocks = vi.hoisted(() => ({
  preferences: [] as Preference[],
  puts: [] as PutCall[],
  workspaces: [] as { id: string; name: string }[],
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: {
    GET: vi.fn(async (path: string) => {
      if (path === '/workspaces/{wsId}/notification-preferences') {
        return {
          data: { preferences: sdkMocks.preferences },
          error: null,
          response: new Response(null, { status: 200 }),
        };
      }
      return { data: null, error: { status: 404 }, response: new Response(null, { status: 400 }) };
    }),
    PUT: vi.fn(
      async (
        path: string,
        init: { params: { path: { wsId: string } }; body: { preferences: Preference[] } },
      ) => {
        sdkMocks.puts.push({
          path,
          wsId: init.params.path.wsId,
          preferences: init.body.preferences,
        });
        sdkMocks.preferences = init.body.preferences;
        return {
          data: { preferences: init.body.preferences },
          error: null,
          response: new Response(null, { status: 200 }),
        };
      },
    ),
    POST: vi.fn(async () => ({
      data: null,
      error: null,
      response: new Response(null, { status: 400 }),
    })),
    PATCH: vi.fn(async () => ({
      data: null,
      error: null,
      response: new Response(null, { status: 400 }),
    })),
    DELETE: vi.fn(async () => ({
      data: null,
      error: null,
      response: new Response(null, { status: 400 }),
    })),
  },
  authSdk: {
    GET: vi.fn(async () => ({
      data: { workspaces: sdkMocks.workspaces },
      error: null,
      response: new Response(null, { status: 200 }),
    })),
    POST: vi.fn(async () => ({
      data: null,
      error: null,
      response: new Response(null, { status: 400 }),
    })),
    PATCH: vi.fn(async () => ({
      data: null,
      error: null,
      response: new Response(null, { status: 400 }),
    })),
    DELETE: vi.fn(async () => ({
      data: null,
      error: null,
      response: new Response(null, { status: 400 }),
    })),
  },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

import NotificationsForm from '../notifications-form';

const WORKSPACE_ID = '01920000-0000-7000-8000-000000000001';

function testI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'settings',
    ns: ['settings'],
    resources: { en: { settings: {} } },
    interpolation: { escapeValue: false },
    parseMissingKeyHandler: (key: string) => key,
    react: { useSuspense: false },
  });
  return instance;
}

function Wrapper({ children }: { children: ReactNode }): ReactElement {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <I18nextProvider i18n={testI18n()}>
      <QueryClientProvider client={client}>
        <Suspense fallback={<div>loading</div>}>{children}</Suspense>
      </QueryClientProvider>
    </I18nextProvider>
  );
}

/** The server's default matrix: in-app delivers, email and push do not. */
function defaultMatrix(): Preference[] {
  const categories = [
    'task.lifecycle',
    'task.comment',
    'task.mention',
    'relation',
    'timebox',
    'ai',
  ];
  const out: Preference[] = [];
  for (const eventCategory of categories) {
    for (const channel of ['in_app', 'email', 'push'] as const) {
      out.push({ eventCategory, channel, muted: channel !== 'in_app' });
    }
  }
  return out;
}

describe('NotificationsForm', () => {
  beforeEach(() => {
    sdkMocks.preferences = defaultMatrix();
    sdkMocks.puts = [];
    sdkMocks.workspaces = [{ id: WORKSPACE_ID, name: 'Test workspace' }];
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('sends a turned-off category to the preferences endpoint as a mute', async () => {
    const user = userEvent.setup();
    render(
      <Wrapper>
        <NotificationsForm />
      </Wrapper>,
    );

    const commentsSwitch = await screen.findByLabelText(
      'notifications.categories.task_comment.label',
    );
    expect(commentsSwitch.getAttribute('aria-checked')).toBe('true');

    await user.click(commentsSwitch);
    await user.click(screen.getByRole('button', { name: 'notifications.save' }));

    await waitFor(() => {
      expect(sdkMocks.puts).toHaveLength(1);
    });

    const call = sdkMocks.puts[0];
    expect(call?.path).toBe('/workspaces/{wsId}/notification-preferences');
    expect(call?.wsId).toBe(WORKSPACE_ID);

    const comment = call?.preferences.find((p) => p.eventCategory === 'task.comment');
    expect(comment).toEqual({ eventCategory: 'task.comment', channel: 'in_app', muted: true });

    // The categories the user did not touch must be submitted unmuted,
    // not dropped: a body that omitted them would leave a previously
    // muted category muted with no way back through this screen.
    const lifecycle = call?.preferences.find((p) => p.eventCategory === 'task.lifecycle');
    expect(lifecycle).toEqual({
      eventCategory: 'task.lifecycle',
      channel: 'in_app',
      muted: false,
    });
  });

  it('renders a muted category as switched off', async () => {
    sdkMocks.preferences = defaultMatrix().map((p) =>
      p.eventCategory === 'ai' && p.channel === 'in_app' ? { ...p, muted: true } : p,
    );

    render(
      <Wrapper>
        <NotificationsForm />
      </Wrapper>,
    );

    const aiSwitch = await screen.findByLabelText('notifications.categories.ai.label');
    expect(aiSwitch.getAttribute('aria-checked')).toBe('false');
    expect(
      screen.getByLabelText('notifications.categories.relation.label').getAttribute('aria-checked'),
    ).toBe('true');
  });
});
