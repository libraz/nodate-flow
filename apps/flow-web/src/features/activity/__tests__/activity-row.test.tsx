import { render } from '@testing-library/react';
import i18next from 'i18next';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { describe, expect, it } from 'vitest';
import ActivityRow from '../activity-row';
import type { ActivityEntry } from '../api';

const USER_ID = '01900000-0000-7000-8000-000000000001';

function buildI18n() {
  const instance = i18next.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'activity',
    resources: { en: { activity: {} } },
    interpolation: { escapeValue: false },
  });
  return instance;
}

function entry(): ActivityEntry {
  return {
    publicId: 'entry-1',
    source: 'audit',
    action: 'task.created',
    actorKind: 'user',
    actorUserPublicId: USER_ID,
    resourceType: 'task',
    severity: 'info',
    occurredAt: 1_700_000_000,
  } as ActivityEntry;
}

function renderRow(actorAvatarUrl?: string) {
  return render(
    <I18nextProvider i18n={buildI18n()}>
      <ul>
        <ActivityRow entry={entry()} actorName="Avery River" actorAvatarUrl={actorAvatarUrl} />
      </ul>
    </I18nextProvider>,
  );
}

describe('ActivityRow actor avatar', () => {
  it('renders initials and no image when the user has no avatar', () => {
    const { container } = renderRow();

    const avatar = container.querySelector<HTMLElement>('[role="img"]');
    if (!avatar) throw new Error('actor avatar was not rendered');
    expect(avatar.dataset.initials).toBe('AR');
    expect(container.querySelector('img')).toBeNull();
  });

  it('resolves a relative avatar URL against the auth api base', () => {
    const { container } = renderRow(`/avatars/${USER_ID}?v=1`);

    expect(container.querySelector('img')?.getAttribute('src')).toBe(
      `http://localhost:8082/avatars/${USER_ID}?v=1`,
    );
  });
});
