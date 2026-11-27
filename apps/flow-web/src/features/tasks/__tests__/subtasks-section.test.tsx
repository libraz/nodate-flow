/**
 * SubtasksSection: progress, row rendering, and the continuous inline add
 * (IME guard, Esc / blur rules, optimistic rows, error restore, dedupe).
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { API_URL, server, useMockApi } from '@tests/msw/server';
import { HttpResponse, http } from 'msw';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const toastMocks = vi.hoisted(() => ({ show: vi.fn() }));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: toastMocks.show },
}));

import { authStore } from '../../auth/auth-store';
import type { TaskListItem } from '../api';
import SubtasksSection from '../subtasks-section';

useMockApi();

function seedAuthToken(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setAccessToken(`${header}.${payload}.`);
}

function child(id: string, overrides: Partial<TaskListItem> = {}): TaskListItem {
  return {
    id,
    projectId: 'prj-1',
    title: `Child ${id}`,
    derivedState: 'open',
    priority: 0,
    primaryAssigneeId: null,
    assigneeCount: 0,
    assignees: [],
    commentCount: 0,
    createdAt: 1_700_000_000,
    sortWeight: 1000,
    subtaskCount: 0,
    taskNumber: 1,
    visibility: 'public',
    ...overrides,
  };
}

let children: TaskListItem[] = [];
let postBodies: unknown[] = [];
let getQueries: URLSearchParams[] = [];

function listHandler() {
  return http.get(`${API_URL}/tasks`, ({ request }) => {
    getQueries.push(new URL(request.url).searchParams);
    return HttpResponse.json({ tasks: children, total: children.length, offset: 0 });
  });
}

function createHandler(gate?: Promise<void>, fail = false) {
  return http.post(`${API_URL}/tasks`, async ({ request }) => {
    const body = (await request.json()) as { title: string };
    postBodies.push(body);
    if (gate) await gate;
    if (fail) {
      return HttpResponse.json({ title: 'boom', status: 500 }, { status: 500 });
    }
    children = [...children, child(`new-${postBodies.length}`, { title: body.title })];
    return HttpResponse.json({ id: `new-${postBodies.length}`, projectId: 'prj-1' });
  });
}

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

async function renderSection() {
  const result = renderWithProviders(
    <Suspense fallback={<p>loading</p>}>
      <SubtasksSection
        taskId="parent-1"
        projectId="prj-1"
        workspaceId="ws-1"
        visibility="project"
        parentTaskId="grand-1"
      />
    </Suspense>,
  );
  await screen.findByRole('heading', { name: 'tasks.detail.subtasks.title' });
  return result;
}

function openAdder(): HTMLInputElement {
  fireEvent.click(screen.getByRole('button', { name: 'tasks.detail.subtasks.add' }));
  return screen.getByRole('textbox', { name: 'tasks.detail.subtasks.input_label' });
}

function typeAndEnter(input: HTMLInputElement, text: string): void {
  fireEvent.change(input, { target: { value: text } });
  fireEvent.keyDown(input, { key: 'Enter' });
}

beforeEach(() => {
  seedAuthToken();
  children = [];
  postBodies = [];
  getQueries = [];
  toastMocks.show.mockReset();
  server.use(listHandler());
});

afterEach(() => {
  authStore.getState().clearSession();
});

describe('<SubtasksSection> list', () => {
  it('requests the direct children of the parent in the workspace', async () => {
    await renderSection();
    const query = getQueries[0];
    expect(query?.get('parentTaskId')).toBe('parent-1');
    expect(query?.get('workspaceId')).toBe('ws-1');
  });

  it('shows only the add row when there are no subtasks', async () => {
    await renderSection();
    expect(screen.queryByRole('list')).toBeNull();
    expect(screen.queryByRole('progressbar')).toBeNull();
    expect(screen.getByRole('button', { name: 'tasks.detail.subtasks.add' })).toBeDefined();
  });

  it('renders rows as links and reports done over total in the progress bar', async () => {
    children = [
      child('a', { derivedState: 'done' }),
      child('b'),
      child('c', {
        assigneeCount: 3,
        assignees: [
          { id: 'u1', displayName: 'Ann' },
          { id: 'u2', displayName: 'Bob' },
          { id: 'u3', displayName: 'Cy' },
        ],
      }),
    ];
    await renderSection();

    expect(screen.getAllByRole('listitem')).toHaveLength(3);
    expect(screen.getByRole('link', { name: 'Child b' }).getAttribute('href')).toBe('/tasks/b');
    const bar = screen.getByRole('progressbar');
    expect(bar.getAttribute('aria-valuenow')).toBe('1');
    expect(bar.getAttribute('aria-valuemax')).toBe('3');
    // Two stacked avatars, the third assignee collapses into the +N label.
    expect(screen.getAllByRole('img')).toHaveLength(2);
    expect(screen.getByText('tasks.assignee.plus_n')).toBeDefined();
  });

  it('marks a finished row as closed and shows its toggle as pressed', async () => {
    children = [child('a', { derivedState: 'done' })];
    await renderSection();
    expect(screen.getByRole('listitem').getAttribute('data-closed')).toBe('true');
    expect(
      screen.getByRole('button', { name: 'today.complete_task' }).getAttribute('aria-pressed'),
    ).toBe('true');
  });
});

describe('<SubtasksSection> inline add', () => {
  it('opens a focused input from the add row and hides the button', async () => {
    await renderSection();
    const input = openAdder();
    expect(document.activeElement).toBe(input);
    expect(screen.queryByRole('button', { name: 'tasks.detail.subtasks.add' })).toBeNull();
    expect(screen.getByText('tasks.detail.subtasks.hint')).toBeDefined();
  });

  it('creates a trimmed title on Enter, clears the input and keeps focus', async () => {
    server.use(createHandler());
    await renderSection();
    const input = openAdder();
    typeAndEnter(input, '  Write tests  ');

    await waitFor(() => {
      expect(postBodies).toEqual([
        {
          parentTaskId: 'parent-1',
          projectId: 'prj-1',
          title: 'Write tests',
          visibility: 'project',
        },
      ]);
    });
    expect(input.value).toBe('');
    expect(document.activeElement).toBe(input);
    expect(await screen.findByRole('link', { name: 'Write tests' })).toBeDefined();
  });

  it('ignores empty input', async () => {
    server.use(createHandler());
    await renderSection();
    const input = openAdder();
    typeAndEnter(input, '   ');
    expect(postBodies).toEqual([]);
  });

  it('ignores Enter while an IME composition is active', async () => {
    server.use(createHandler());
    await renderSection();
    const input = openAdder();
    fireEvent.change(input, { target: { value: 'にほんご' } });
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(input, { key: 'Enter', keyCode: 229 });
    expect(postBodies).toEqual([]);
    expect(input.value).toBe('にほんご');
  });

  it('shows a muted row while the request is in flight and lets the next title be typed', async () => {
    const gate = deferred();
    server.use(createHandler(gate.promise));
    await renderSection();
    const input = openAdder();
    typeAndEnter(input, 'First');

    expect(await screen.findByText('First')).toBeDefined();
    expect(screen.getByRole('status', { name: 'tasks.detail.subtasks.creating' })).toBeDefined();
    // The input stays usable while the create is pending.
    expect(input.disabled).toBe(false);
    fireEvent.change(input, { target: { value: 'Second' } });
    expect(input.value).toBe('Second');

    gate.resolve();
    await waitFor(() => {
      expect(screen.queryByRole('status', { name: 'tasks.detail.subtasks.creating' })).toBeNull();
    });
    expect(screen.getByRole('link', { name: 'First' })).toBeDefined();
  });

  it('does not send the same title twice while it is in flight', async () => {
    const gate = deferred();
    server.use(createHandler(gate.promise));
    await renderSection();
    const input = openAdder();
    typeAndEnter(input, 'Same');
    typeAndEnter(input, 'Same');

    await waitFor(() => {
      expect(postBodies).toHaveLength(1);
    });
    gate.resolve();
  });

  it('toasts the error and restores the text when the input is empty', async () => {
    server.use(createHandler(undefined, true));
    await renderSection();
    const input = openAdder();
    typeAndEnter(input, 'Doomed');

    await waitFor(() => {
      expect(toastMocks.show).toHaveBeenCalledTimes(1);
    });
    expect(toastMocks.show.mock.calls[0]?.[0]).toMatchObject({ tone: 'danger' });
    expect(input.value).toBe('Doomed');
    expect(screen.queryByRole('status', { name: 'tasks.detail.subtasks.creating' })).toBeNull();
  });

  it('closes on Esc, clears the text and returns focus to the add button', async () => {
    await renderSection();
    const input = openAdder();
    fireEvent.change(input, { target: { value: 'draft' } });
    fireEvent.keyDown(input, { key: 'Escape' });

    const button = screen.getByRole('button', { name: 'tasks.detail.subtasks.add' });
    expect(document.activeElement).toBe(button);
    fireEvent.click(button);
    expect(
      (
        screen.getByRole('textbox', {
          name: 'tasks.detail.subtasks.input_label',
        }) as HTMLInputElement
      ).value,
    ).toBe('');
  });

  it('closes on blur when empty and stays open on blur with text', async () => {
    await renderSection();
    const input = openAdder();
    fireEvent.change(input, { target: { value: 'keep me' } });
    fireEvent.blur(input);
    expect(
      screen.getByRole('textbox', { name: 'tasks.detail.subtasks.input_label' }),
    ).toBeDefined();

    fireEvent.change(input, { target: { value: '' } });
    fireEvent.blur(input);
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.getByRole('button', { name: 'tasks.detail.subtasks.add' })).toBeDefined();
  });
});

describe('<SubtasksSection> parent changes', () => {
  let patches: { url: string; body: unknown }[] = [];

  function patchHandler(fail = false) {
    return http.patch(`${API_URL}/tasks/:id`, async ({ request, params }) => {
      patches.push({ url: String(params.id), body: await request.json() });
      if (fail) {
        return HttpResponse.json({ title: 'boom', status: 422 }, { status: 422 });
      }
      children = children.filter((c) => c.id !== params.id);
      return HttpResponse.json({ id: params.id, projectId: 'prj-1' });
    });
  }

  beforeEach(() => {
    patches = [];
  });

  it('detaches a row with an empty parent id and drops it from the list', async () => {
    children = [child('a'), child('b')];
    server.use(patchHandler());
    await renderSection();
    fireEvent.click(
      screen.getAllByRole('button', {
        name: 'tasks.detail.subtasks.detach_named',
      })[0] as HTMLElement,
    );
    await waitFor(() => {
      expect(patches).toEqual([{ url: 'a', body: { parentTaskId: '' } }]);
    });
    await waitFor(() => {
      expect(screen.queryByRole('link', { name: 'Child a' })).toBeNull();
    });
  });

  it('toasts when detaching fails', async () => {
    children = [child('a')];
    server.use(patchHandler(true));
    await renderSection();
    fireEvent.click(screen.getByRole('button', { name: 'tasks.detail.subtasks.detach_named' }));
    await waitFor(() => {
      expect(toastMocks.show).toHaveBeenCalledTimes(1);
    });
    expect(screen.getByRole('link', { name: 'Child a' })).toBeDefined();
  });

  it('attaches an existing task from the search, excluding itself, its parent and its children', async () => {
    children = [child('kid', { title: 'Already here' })];
    server.use(
      patchHandler(),
      http.get(`${API_URL}/tasks`, ({ request }) => {
        const q = new URL(request.url).searchParams;
        if (q.get('q')) {
          return HttpResponse.json({
            tasks: [
              child('parent-1', { title: 'Match self' }),
              child('grand-1', { title: 'Match parent' }),
              child('kid', { title: 'Match child' }),
              child('other-prj', { title: 'Match elsewhere', projectId: 'prj-2' }),
              child('free', { title: 'Match free' }),
            ],
            total: 5,
            offset: 0,
          });
        }
        return HttpResponse.json({ tasks: children, total: children.length, offset: 0 });
      }),
    );
    await renderSection();
    fireEvent.click(screen.getByRole('button', { name: 'tasks.detail.subtasks.add_existing' }));
    fireEvent.change(
      screen.getByRole('textbox', { name: 'tasks.detail.subtasks.existing_placeholder' }),
      {
        target: { value: 'Match' },
      },
    );

    const pick = await screen.findByRole('button', { name: /Match free/ });
    expect(screen.queryByRole('button', { name: /Match self/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /Match parent/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /Match child/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /Match elsewhere/ })).toBeNull();

    fireEvent.click(pick);
    await waitFor(() => {
      expect(patches).toEqual([{ url: 'free', body: { parentTaskId: 'parent-1' } }]);
    });
    await waitFor(() => {
      expect(screen.queryByRole('textbox')).toBeNull();
    });
  });
});
