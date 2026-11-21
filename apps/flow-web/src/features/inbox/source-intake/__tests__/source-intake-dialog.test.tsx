import { QueryClient } from '@tanstack/react-query';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { authStore } from '../../../auth/auth-store';
import { type Project, projectsKeys } from '../../../projects/api';
import TaskSourceLinks from '../../../tasks/task-source-links';
import { type WorkspaceMember, workspacesKeys } from '../../../workspaces/api';
import type { InboxItem } from '../../api';
import InboxItemRow from '../../inbox-item-row';
import SourceIntakeDialog from '../source-intake-dialog';

vi.mock('react-i18next', async () => {
  const actual = await vi.importActual<typeof import('react-i18next')>('react-i18next');
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string, options?: { returnObjects?: boolean }) => {
        if (options?.returnObjects && key === 'common.date.weekdays') {
          return ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
        }
        return key;
      },
      i18n: { resolvedLanguage: 'en' },
    }),
  };
});

useMockApi();

const inboxItem = {
  id: 'signal-1',
  workspaceId: 'ws-1',
  source: 'github',
  kind: 'pull_request.opened',
  receivedAt: 1_700_000_000,
  payload: JSON.stringify({ secretRawPayload: 'must not be displayed' }),
  sourceDetail: {
    title: 'Review the release handoff',
    body: 'Please check the release notes before publishing.',
    sourceUrl: 'https://github.com/acme/service/pull/17',
  },
} as unknown as InboxItem;

const project = { id: 'project-1', name: 'Platform', identifier: 'PLAT' } as Project;
const archivedProject = {
  id: 'project-old',
  name: 'Retired',
  identifier: 'OLD',
  isArchived: true,
} as Project;
const member = {
  id: 'member-1',
  userId: 'user-1',
  displayName: 'Avery Example',
  email: 'avery@example.test',
  role: 'member',
} as WorkspaceMember;

interface SourceApiRequests {
  capture: Record<string, unknown>[];
  convert: Record<string, unknown>[];
  sourceListFetches: { count: number };
}

function installSourceHandlers(
  options: {
    convertStatuses?: number[];
    captureStatus?: number;
    captureGate?: Promise<void>;
    onCaptureStarted?: () => void;
  } = {},
) {
  const requests: SourceApiRequests = {
    capture: [],
    convert: [],
    sourceListFetches: { count: 0 },
  };
  const convertStatuses = [...(options.convertStatuses ?? [])];
  server.use(
    http.post(`${API_URL}/workspaces/ws-1/inbox/signal-1/intake`, async ({ request }) => {
      requests.capture.push((await request.json()) as Record<string, unknown>);
      options.onCaptureStarted?.();
      if (options.captureGate) await options.captureGate;
      if (options.captureStatus && options.captureStatus >= 400) {
        return HttpResponse.json(
          { code: 'WS.TASK.ACCESS_DENIED', message: 'Access denied' },
          { status: options.captureStatus },
        );
      }
      const requestBody = requests.capture.at(-1);
      return HttpResponse.json({
        intakeItemId: 'intake-1',
        title: inboxItem.sourceDetail.title,
        body: inboxItem.sourceDetail.body,
        sourceUrl: inboxItem.sourceDetail.sourceUrl,
        ...(requestBody?.existingTaskId ? { taskId: requestBody.existingTaskId } : {}),
        created: true,
      });
    }),
    http.post(`${API_URL}/workspaces/ws-1/intake/intake-1/convert`, async ({ request }) => {
      requests.convert.push((await request.json()) as Record<string, unknown>);
      const status = convertStatuses.shift();
      if (status && status >= 400) {
        return HttpResponse.json(
          { code: 'INTERNAL.UNEXPECTED', message: 'Temporary failure' },
          { status },
        );
      }
      return HttpResponse.json({ ok: true, taskId: 'task-new' });
    }),
    http.get(`${API_URL}/tasks`, () =>
      HttpResponse.json({
        tasks: [{ id: 'task-existing', title: 'Existing release task', derivedState: 'open' }],
        total: 1,
      }),
    ),
    http.get(`${API_URL}/tasks/task-existing/sources`, () => {
      requests.sourceListFetches.count += 1;
      const linked = requests.capture.some((request) => request.existingTaskId === 'task-existing');
      return HttpResponse.json({
        sources: linked
          ? [
              {
                intakeItemId: 'intake-1',
                signalId: 'signal-1',
                source: 'github',
                title: inboxItem.sourceDetail.title,
                body: inboxItem.sourceDetail.body,
                sourceUrl: inboxItem.sourceDetail.sourceUrl,
                createdAt: 1_700_000_000,
              },
            ]
          : [],
      });
    }),
  );
  return requests;
}

function createSourceQueryClient(): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
  });
  queryClient.setQueryData(projectsKeys.list('ws-1'), [project, archivedProject]);
  queryClient.setQueryData(projectsKeys.list('ws-2'), [project]);
  queryClient.setQueryData(workspacesKeys.members('ws-1'), [member]);
  queryClient.setQueryData(workspacesKeys.members('ws-2'), [member]);
  return queryClient;
}

function renderDialog(
  item: InboxItem = inboxItem,
  workspaceId = 'ws-1',
  includeTaskSources = false,
): ReturnType<typeof renderWithProviders> {
  return renderWithProviders(
    <>
      <Suspense fallback={<p>loading</p>}>
        <SourceIntakeDialog workspaceId={workspaceId} item={item} open={true} onClose={vi.fn()} />
      </Suspense>
      {includeTaskSources ? (
        <Suspense fallback={<p>loading sources</p>}>
          <TaskSourceLinks taskId="task-existing" />
        </Suspense>
      ) : null}
    </>,
    { queryClient: createSourceQueryClient() },
  );
}

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

async function chooseDueDate(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByRole('button', { name: 'common.date.placeholder' }));
  await user.click(await screen.findByRole('button', { name: /^15$/ }));
}

beforeEach(() => {
  seedAuthToken();
});

afterEach(() => {
  authStore.getState().clearSession();
});

describe('SourceIntakeDialog API workflow', () => {
  it('opens the source workflow from the inbox row', async () => {
    const user = userEvent.setup();
    renderWithProviders(<InboxItemRow item={inboxItem} onArchive={vi.fn()} onSnooze={vi.fn()} />, {
      queryClient: createSourceQueryClient(),
    });

    expect(screen.getByText('Review the release handoff')).toBeDefined();
    await user.click(screen.getByRole('button', { name: 'sourceIntake.review' }));

    expect(await screen.findByRole('dialog', { name: 'sourceIntake.title' })).toBeDefined();
    expect(screen.getByDisplayValue('Review the release handoff')).toBeDefined();
  });

  it('attaches a missing-title error to the title field', async () => {
    const user = userEvent.setup();
    const requests = installSourceHandlers();
    renderDialog();

    const titleInput = await screen.findByRole('textbox', { name: /sourceIntake\.task_title/ });
    await user.clear(titleInput);
    await user.click(screen.getByRole('button', { name: 'sourceIntake.create_task' }));

    expect(titleInput.getAttribute('aria-invalid')).toBe('true');
    const describedBy = titleInput.getAttribute('aria-describedby') ?? '';
    expect(describedBy).not.toBe('');
    expect(
      describedBy
        .split(' ')
        .map((id) => document.getElementById(id)?.textContent)
        .join(' '),
    ).toContain('sourceIntake.title_required');
    expect(requests.convert).toHaveLength(0);

    await user.type(titleInput, 'Retitled');
    expect(titleInput.getAttribute('aria-invalid')).toBeNull();
  });

  it('attaches a missing-project error to the project field', async () => {
    const user = userEvent.setup();
    const requests = installSourceHandlers();
    const queryClient = createSourceQueryClient();
    queryClient.setQueryData(projectsKeys.list('ws-1'), []);
    renderWithProviders(
      <Suspense fallback={<p>loading</p>}>
        <SourceIntakeDialog workspaceId="ws-1" item={inboxItem} open={true} onClose={vi.fn()} />
      </Suspense>,
      { queryClient },
    );

    await user.click(await screen.findByRole('button', { name: 'sourceIntake.create_task' }));

    const projectSelect = screen.getByRole('combobox', { name: /intake\.convert\.project_label/ });
    expect(projectSelect.getAttribute('aria-invalid')).toBe('true');
    expect(
      (projectSelect.getAttribute('aria-describedby') ?? '')
        .split(' ')
        .map((id) => document.getElementById(id)?.textContent)
        .join(' '),
    ).toContain('sourceIntake.project_required');
    expect(requests.convert).toHaveLength(0);
  });

  it('does not offer archived projects as a destination', async () => {
    installSourceHandlers();
    renderDialog();

    const projectSelect = await screen.findByRole('combobox', {
      name: /intake\.convert\.project_label/,
    });
    expect(projectSelect.textContent).toContain('Platform');
    expect(projectSelect.textContent).not.toContain('Retired');
  });

  it('exposes the two task destinations as a radio group', async () => {
    const user = userEvent.setup();
    installSourceHandlers();
    renderDialog();

    const destinationGroup = await screen.findByRole('radiogroup', {
      name: 'sourceIntake.destination',
    });
    expect(destinationGroup).toBeDefined();
    expect(
      screen
        .getByRole('radio', { name: 'sourceIntake.create_destination' })
        .getAttribute('aria-checked'),
    ).toBe('true');
    const existingDestination = screen.getByRole('radio', {
      name: 'sourceIntake.existing_destination',
    });
    expect(existingDestination).toBeDefined();

    await user.click(existingDestination);
    expect(screen.getByRole('textbox', { name: 'sourceIntake.task_search' })).toBeDefined();
  });

  it('shows the safe source projection and creates a task with source title, body, assignee, and due date', async () => {
    const user = userEvent.setup();
    const requests = installSourceHandlers();
    const { container } = renderDialog();

    expect(screen.getByDisplayValue('Review the release handoff')).toBeDefined();
    expect(
      screen.getByDisplayValue('Please check the release notes before publishing.'),
    ).toBeDefined();
    expect(
      screen.getByRole('link', { name: 'sourceIntake.open_source' }).getAttribute('href'),
    ).toBe('https://github.com/acme/service/pull/17');
    expect(container.textContent).not.toContain('must not be displayed');
    expect(container.querySelector('script')).toBeNull();

    await user.click(screen.getByRole('checkbox', { name: 'Avery Example' }));
    await chooseDueDate(user);
    await user.click(screen.getByRole('button', { name: 'sourceIntake.create_task' }));

    await waitFor(() => expect(requests.convert).toHaveLength(1));
    expect(requests.capture).toEqual([{}]);
    expect(requests.convert[0]).toEqual({
      projectId: 'project-1',
      title: 'Review the release handoff',
      body: 'Please check the release notes before publishing.',
      assigneeUserIds: ['user-1'],
      dueOn: expect.stringMatching(/^\d{4}-\d{2}-15$/),
    });
    expect(
      screen.getByRole('link', { name: 'sourceIntake.open_task' }).getAttribute('href'),
    ).toContain('task-new');
  });

  it('retries a failed conversion with the same intake item and retained form values', async () => {
    const user = userEvent.setup();
    const requests = installSourceHandlers({ convertStatuses: [500, 200] });
    renderDialog();

    await user.click(screen.getByRole('button', { name: 'sourceIntake.create_task' }));
    await waitFor(() => expect(screen.getByRole('alert')).toBeDefined());
    expect(screen.getByDisplayValue('Review the release handoff')).toBeDefined();
    expect(requests.capture).toHaveLength(1);
    expect(requests.convert).toHaveLength(1);

    await user.click(screen.getByRole('button', { name: 'sourceIntake.create_task' }));
    await waitFor(() => expect(requests.convert).toHaveLength(2));

    expect(requests.capture).toHaveLength(1);
    expect(requests.convert[1]).toEqual(requests.convert[0]);
    expect(screen.getByRole('link', { name: 'sourceIntake.open_task' })).toBeDefined();
  });

  it('keeps the original workspace and source when the workspace changes during capture', async () => {
    const user = userEvent.setup();
    let releaseCapture: (() => void) | undefined;
    let captureStarted: (() => void) | undefined;
    const captureGate = new Promise<void>((resolve) => {
      releaseCapture = resolve;
    });
    const captureStartedPromise = new Promise<void>((resolve) => {
      captureStarted = resolve;
    });
    const requests = installSourceHandlers({
      captureGate,
      onCaptureStarted: () => captureStarted?.(),
    });
    let workspaceTwoCaptureCount = 0;
    let workspaceTwoConvertCount = 0;
    server.use(
      http.post(`${API_URL}/workspaces/ws-2/inbox/signal-2/intake`, () => {
        workspaceTwoCaptureCount += 1;
        return HttpResponse.json({
          intakeItemId: 'wrong-intake',
          title: 'Wrong source',
          created: true,
        });
      }),
      http.post(`${API_URL}/workspaces/ws-2/intake/:id/convert`, () => {
        workspaceTwoConvertCount += 1;
        return HttpResponse.json({ ok: true, taskId: 'wrong-task' });
      }),
    );
    const view = renderDialog();

    await user.click(screen.getByRole('button', { name: 'sourceIntake.create_task' }));
    await captureStartedPromise;

    const nextItem = {
      ...inboxItem,
      id: 'signal-2',
      workspaceId: 'ws-2',
      sourceDetail: {
        title: 'A different source after switching workspaces',
        body: 'This source must not replace the in-flight request.',
      },
    } as InboxItem;
    view.rerender(
      <Suspense fallback={<p>loading</p>}>
        <SourceIntakeDialog workspaceId="ws-2" item={nextItem} open={true} onClose={vi.fn()} />
      </Suspense>,
    );
    releaseCapture?.();

    await waitFor(() => expect(requests.convert).toHaveLength(1));
    expect(requests.capture).toEqual([{}]);
    expect(requests.convert[0]).toEqual({
      projectId: 'project-1',
      title: 'Review the release handoff',
      body: 'Please check the release notes before publishing.',
      assigneeUserIds: [],
    });
    expect(workspaceTwoCaptureCount).toBe(0);
    expect(workspaceTwoConvertCount).toBe(0);
    expect(screen.queryByRole('link', { name: 'sourceIntake.open_task' })).toBeNull();
  });

  it('retains the selected task and source details when the link request is denied by ACL', async () => {
    const user = userEvent.setup();
    const requests = installSourceHandlers({ captureStatus: 403 });
    renderDialog();

    await user.click(screen.getByRole('radio', { name: 'sourceIntake.existing_destination' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'sourceIntake.task_search' }), {
      target: { value: 'release' },
    });
    await user.click(await screen.findByRole('button', { name: 'Existing release task' }));
    await user.click(screen.getByRole('button', { name: 'sourceIntake.link_task' }));

    await waitFor(() => expect(screen.getByRole('alert')).toBeDefined());
    expect(requests.capture).toEqual([{ existingTaskId: 'task-existing' }]);
    expect(requests.convert).toHaveLength(0);
    expect(screen.getByText('Review the release handoff')).toBeDefined();
    expect(screen.getByText('Existing release task')).toBeDefined();
  });

  it('refreshes an active task source query after linking a source', async () => {
    const user = userEvent.setup();
    const requests = installSourceHandlers();
    const view = renderDialog(inboxItem, 'ws-1', true);

    await waitFor(() => expect(requests.sourceListFetches.count).toBeGreaterThan(0));
    const initialSourceFetches = requests.sourceListFetches.count;
    await user.click(screen.getByRole('radio', { name: 'sourceIntake.existing_destination' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'sourceIntake.task_search' }), {
      target: { value: 'release' },
    });
    await user.click(await screen.findByRole('button', { name: 'Existing release task' }));
    await user.click(screen.getByRole('button', { name: 'sourceIntake.link_task' }));

    expect(await screen.findByRole('link', { name: 'sourceIntake.open_task' })).toBeDefined();
    await waitFor(() =>
      expect(requests.sourceListFetches.count).toBeGreaterThan(initialSourceFetches),
    );
    expect(requests.capture).toEqual([{ existingTaskId: 'task-existing' }]);

    view.rerender(
      <>
        <Suspense fallback={<p>loading</p>}>
          <SourceIntakeDialog workspaceId="ws-1" item={inboxItem} open={false} onClose={vi.fn()} />
        </Suspense>
        <Suspense fallback={<p>loading sources</p>}>
          <TaskSourceLinks taskId="task-existing" />
        </Suspense>
      </>,
    );
    expect(await screen.findByRole('heading', { name: 'sourceIntake.task_sources' })).toBeDefined();
  });

  it('omits a source link when the source URL is not safe to navigate to', () => {
    const unsafeItem = {
      ...inboxItem,
      sourceDetail: { ...inboxItem.sourceDetail, sourceUrl: 'javascript:alert(1)' },
    } as InboxItem;
    renderDialog(unsafeItem);

    expect(screen.queryByRole('link', { name: 'sourceIntake.open_source' })).toBeNull();
  });
});
