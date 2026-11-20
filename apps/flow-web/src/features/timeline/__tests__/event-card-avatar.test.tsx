import type { components } from '@nodate-flow/sdk';
import { render } from '@testing-library/react';
import i18next from 'i18next';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import EventCard, { resolveActorAvatarUrl } from '../event-card';

type Event = components['schemas']['Event'];
type EventOverrides = { [Key in keyof Event]?: Event[Key] | undefined };

function buildI18n() {
  const instance = i18next.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: {
      en: {
        timeline: {
          actor: { system: 'System', initials_fallback: '?' },
        },
      },
    },
    interpolation: { escapeValue: false },
  });
  return instance;
}

function event(overrides: EventOverrides = {}): Event {
  return {
    id: 'event-1',
    type: 'task.created',
    actorUserId: '01900000-0000-7000-8000-000000000001',
    actorDisplayName: 'Avery River',
    occurredAt: 1_700_000_000,
    wasReversed: false,
    ...overrides,
  } as Event;
}

function renderEventCard(value: Event) {
  return render(
    <I18nextProvider i18n={buildI18n()}>
      <EventCard event={value} />
    </I18nextProvider>,
  );
}

describe('EventCard actor avatar', () => {
  it('keeps the configured auth base path and query when prefixing an uploaded URL', () => {
    expect(
      resolveActorAvatarUrl(
        '/avatars/01900000-0000-7000-8000-000000000001?v=deadbeef',
        'https://auth.example.test/identity/v2?tenant=green',
      ),
    ).toBe(
      'https://auth.example.test/identity/v2/avatars/01900000-0000-7000-8000-000000000001?tenant=green&v=deadbeef',
    );
  });

  it('returns external URLs without rewriting them', () => {
    const externalURL = 'HTTPS://images.example.test/a.png?size=96';
    expect(resolveActorAvatarUrl(externalURL, 'https://auth.example.test/identity')).toBe(
      externalURL,
    );
  });

  it('keeps initials and does not request an avatar when the API has none', () => {
    const { container } = renderEventCard(event());

    const avatar = container.querySelector<HTMLElement>('[role="img"]');
    if (!avatar) throw new Error('actor avatar was not rendered');
    expect(avatar.getAttribute('aria-label')).toBe('Avery River');
    expect(avatar.textContent).toContain('AR');
    expect(avatar.querySelector('img')).toBeNull();
  });

  it('renders a server supplied uploaded URL with its cache tag', () => {
    const { container } = renderEventCard(
      event({
        actorAvatarUrl: '/avatars/01900000-0000-7000-8000-000000000001?v=deadbeef',
      }),
    );

    expect(container.querySelector('[role="img"] img')?.getAttribute('src')).toBe(
      'http://localhost:8082/avatars/01900000-0000-7000-8000-000000000001?v=deadbeef',
    );
  });

  it('passes an external server supplied URL through unchanged', () => {
    const externalURL = 'https://images.example.test/a.png?size=96';
    const { container } = renderEventCard(event({ actorAvatarUrl: externalURL }));

    expect(container.querySelector('[role="img"] img')?.getAttribute('src')).toBe(externalURL);
  });

  it('keeps the agent glyph and does not render a user image', () => {
    const { container } = renderEventCard(
      event({
        actorAgentId: '01900000-0000-7000-8000-000000000002',
        actorAgentName: 'Planner',
        actorAvatarUrl: '/avatars/01900000-0000-7000-8000-000000000001?v=deadbeef',
      }),
    );

    expect(container.querySelector('[role="img"]')?.getAttribute('aria-label')).toBe(
      'actor.actor_agent',
    );
    expect(container.querySelector('img')).toBeNull();
  });

  it('shows a labelled system glyph, not a "?" avatar, when there is no user actor', () => {
    const { container } = renderEventCard(
      event({
        actorUserId: undefined,
        actorDisplayName: undefined,
        actorAvatarUrl: undefined,
      }),
    );

    const avatar = container.querySelector<HTMLElement>('[role="img"]');
    if (!avatar) throw new Error('system avatar was not rendered');
    expect(avatar.getAttribute('aria-label')).toBe('System');
    expect(avatar.textContent).not.toContain('?');
    expect(avatar.querySelector('svg')).not.toBeNull();
    expect(container.querySelector('img')).toBeNull();
  });
});
