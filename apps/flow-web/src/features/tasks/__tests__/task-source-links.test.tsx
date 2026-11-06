import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { authStore } from '../../auth/auth-store';
import TaskSourceLinks from '../task-source-links';

useMockApi();

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

beforeEach(seedAuthToken);

afterEach(() => {
  authStore.getState().clearSession();
});

describe('<TaskSourceLinks>', () => {
  it('loads source backlinks and renders only safe source URLs as links', async () => {
    server.use(
      http.get(`${API_URL}/tasks/task-1/sources`, () =>
        HttpResponse.json({
          sources: [
            {
              intakeItemId: 'intake-1',
              signalId: 'signal-1',
              source: 'github',
              kind: 'pull_request.opened',
              title: 'Review the release handoff',
              body: 'Please review these notes.',
              sourceUrl: 'https://github.com/acme/service/pull/17',
              createdAt: 1_700_000_000,
            },
            {
              intakeItemId: 'intake-2',
              source: 'github',
              title: 'Unsafe source',
              sourceUrl: 'javascript:alert(1)',
              createdAt: 1_700_000_100,
            },
          ],
        }),
      ),
    );
    renderWithProviders(
      <Suspense fallback={<p>loading</p>}>
        <TaskSourceLinks taskId="task-1" />
      </Suspense>,
    );

    expect(await screen.findByText('Review the release handoff')).toBeDefined();
    expect(screen.getByText('Please review these notes.')).toBeDefined();
    expect(screen.getByText('Unsafe source')).toBeDefined();
    const sourceLinks = screen.getAllByRole('link', { name: 'sourceIntake.open_source' });
    expect(sourceLinks).toHaveLength(1);
    expect(sourceLinks[0]?.getAttribute('href')).toBe('https://github.com/acme/service/pull/17');
    await waitFor(() => expect(screen.queryByRole('link', { name: /javascript:/i })).toBeNull());
  });
});
