#!/usr/bin/env node
// Entry point for the `tnk` CLI.

import { c, createCLI, prompt, table } from '@libraz/node-cli';

import { createAuthClient, createFlowClient } from './api.js';
import { clearCredentials, getFlowApiUrl, loadCredentials, saveCredentials } from './config.js';
import {
  STATE_TRANSITIONS,
  type SdkClientLike,
  type SearchOptions as SearchOptionsInput,
  type UpdateOptions as UpdateOptionsInput,
  buildSearchQuery,
  buildUpdatePlan,
  executeSearch,
  executeUpdate,
} from './task-builders.js';

const cli = createCLI({
  name: 'tnk',
  version: '0.0.0',
  description: 'nodate-flow CLI',
});

// ---------------------------------------------------------------------------
// tnk auth login
// ---------------------------------------------------------------------------
const auth = cli.command('auth').description('Authentication commands');

auth
  .command('login')
  .description('Log in with email and password')
  .action(async ({ stdout, stderr }) => {
    const email = await prompt.text('Email:');
    const password = await prompt.password('Password:');

    const client = createAuthClient();
    const { data, error } = await client.POST('/auth/login', {
      body: { email, password },
    });

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Login failed';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = 1;
      return;
    }

    if (data.step === 'totp_required') {
      stderr.write(c`{yellow TOTP required}. Use the web UI to complete login.\n`);
      process.exitCode = 1;
      return;
    }

    if (!data.accessToken) {
      stderr.write(c`{red Error}: No access token in response\n`);
      process.exitCode = 1;
      return;
    }

    saveCredentials({
      accessToken: data.accessToken,
      refreshToken: '',
      apiBaseUrl: getFlowApiUrl(),
    });

    stdout.write(c`{green Logged in} as ${email}\n`);
    stdout.write('Credentials saved to ~/.config/tnk/credentials.json\n');
  });

// ---------------------------------------------------------------------------
// tnk auth logout
// ---------------------------------------------------------------------------
auth
  .command('logout')
  .description('Clear stored credentials')
  .action(({ stdout }) => {
    clearCredentials();
    stdout.write('Logged out. Credentials removed.\n');
  });

// ---------------------------------------------------------------------------
// tnk auth status
// ---------------------------------------------------------------------------
auth
  .command('status')
  .description('Show current authentication status')
  .action(({ stdout }) => {
    const creds = loadCredentials();
    if (creds) {
      stdout.write(c`{green Authenticated}\n`);
      stdout.write(`API: ${creds.apiBaseUrl}\n`);
    } else {
      stdout.write(c`{dim Not logged in}\n`);
    }
  });

// ---------------------------------------------------------------------------
// tnk task list
// ---------------------------------------------------------------------------
const task = cli.command('task').description('Task commands');

task
  .command('list')
  .description('List tasks')
  .option('-p, --project <projectId>', {
    description: 'Filter by project public id',
    type: 'string',
  })
  .option('-s, --status <status>', {
    description: 'Filter by derived state',
    type: 'string',
  })
  .option('-w, --workspace <workspaceId>', {
    description: 'Workspace public id',
    type: 'string',
  })
  .option('-l, --limit <limit>', {
    description: 'Maximum number of tasks to return',
    type: 'number',
    default: 25,
  })
  .action(async ({ options, stdout, stderr }) => {
    const projectId = options.project as string | undefined;
    const status = options.status as string | undefined;
    const workspaceId = options.workspace as string | undefined;
    const limit = options.limit as number;

    const client = createFlowClient();

    const query: Record<string, unknown> = { limit };
    if (projectId) query.projectId = projectId;
    if (workspaceId) query.workspaceId = workspaceId;
    if (status) query.state = [status];

    // biome-ignore lint/suspicious/noExplicitAny: query is built dynamically
    const { data, error } = await client.GET('/tasks', { params: { query } } as any);

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Failed to list tasks';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = 1;
      return;
    }

    const tasks = data.tasks;
    if (!tasks || tasks.length === 0) {
      stdout.write('No tasks found.\n');
      return;
    }

    const rows = tasks.map(
      (t: {
        id: string;
        title: string;
        derivedState: string;
        priority: number;
        dueOn?: string | null;
      }) => ({
        id: t.id.slice(0, 8),
        title: t.title.length > 50 ? `${t.title.slice(0, 47)}...` : t.title,
        state: t.derivedState,
        priority: String(t.priority),
        due: t.dueOn ?? '-',
      }),
    );

    const output = table(rows, {
      columns: ['id', 'title', 'state', 'priority', 'due'],
      headerLabels: {
        id: 'ID',
        title: 'Title',
        state: 'State',
        priority: 'Pri',
        due: 'Due',
      },
      border: 'simple',
    });

    stdout.write(output);
    stdout.write(`\n${data.total} task(s) total\n`);
  });

// ---------------------------------------------------------------------------
// tnk task create
// ---------------------------------------------------------------------------
task
  .command('create')
  .description('Create a task')
  .option('-t, --title <title>', {
    description: 'Task title (required)',
    type: 'string',
    required: true,
  })
  .option('-d, --description <description>', {
    description: 'Task description',
    type: 'string',
  })
  .option('-p, --project <projectId>', {
    description: 'Project public id (required)',
    type: 'string',
    required: true,
  })
  .option('--due <date>', {
    description: 'Due date (YYYY-MM-DD)',
    type: 'string',
  })
  .option('--start <date>', {
    description: 'Start date (YYYY-MM-DD)',
    type: 'string',
  })
  .option('--priority <priority>', {
    description: 'Priority (0-3)',
    type: 'number',
    default: 0,
  })
  .option('--visibility <visibility>', {
    description: 'Visibility: public, project, or private',
    type: 'string',
    default: 'public',
    choices: ['public', 'project', 'private'],
  })
  .action(async ({ options, stdout, stderr }) => {
    const title = options.title as string;
    const projectId = options.project as string;
    const description = options.description as string | undefined;
    const dueOn = options.due as string | undefined;
    const startOn = options.start as string | undefined;
    const priority = options.priority as number;
    const visibility = (options.visibility as 'public' | 'project' | 'private') ?? 'public';

    const client = createFlowClient();

    const body: Record<string, unknown> = {
      title,
      projectId,
      visibility,
      priority,
    };
    if (description) body.description = description;
    if (dueOn) body.dueOn = dueOn;
    if (startOn) body.startOn = startOn;

    // biome-ignore lint/suspicious/noExplicitAny: body is built dynamically
    const { data, error } = await client.POST('/tasks', { body } as any);

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Failed to create task';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = 1;
      return;
    }

    stdout.write(c`{green Created} task ${data.id}\n`);
    stdout.write(`Title: ${data.title}\n`);
  });

// ---------------------------------------------------------------------------
// tnk task update <id>
// ---------------------------------------------------------------------------
//
// The `--state` flag accepts a state-machine transition name (see
// STATE_TRANSITIONS). State changes are not part of `PATCH /tasks/{id}`
// on the server, so when both patch fields and a state transition are
// supplied we issue the patch first and the transition second. See
// `task-builders.ts` for the pure builder + executor used here.

task
  .command('update <id>')
  .description('Update a task')
  .option('-t, --title <title>', {
    description: 'New title',
    type: 'string',
  })
  .option('-d, --description <description>', {
    description: 'New description',
    type: 'string',
  })
  .option('--due <date>', {
    description: 'Due date (YYYY-MM-DD, empty to clear)',
    type: 'string',
  })
  .option('--start <date>', {
    description: 'Start date (YYYY-MM-DD, empty to clear)',
    type: 'string',
  })
  .option('--priority <priority>', {
    description: 'Priority (0-3)',
    type: 'number',
  })
  .option('--state <state>', {
    description:
      'Apply a state-machine transition: start, block, unblock, submit, complete, reopen, cancel',
    type: 'string',
    choices: [...STATE_TRANSITIONS],
  })
  .option('--visibility <visibility>', {
    description: 'Visibility: public, project, or private',
    type: 'string',
    choices: ['public', 'project', 'private'],
  })
  .action(async ({ args, options, stdout, stderr }) => {
    const id = args.id as string;

    // Build the options object with `exactOptionalPropertyTypes`-friendly
    // property assignments so each key is omitted entirely when the user
    // didn't pass the matching flag.
    const updateOptions: UpdateOptionsInput = {};
    if (options.title !== undefined) updateOptions.title = options.title as string;
    if (options.description !== undefined)
      updateOptions.description = options.description as string;
    if (options.due !== undefined) updateOptions.due = options.due as string;
    if (options.start !== undefined) updateOptions.start = options.start as string;
    if (options.priority !== undefined) updateOptions.priority = options.priority as number;
    if (options.state !== undefined) updateOptions.state = options.state as string;
    if (options.visibility !== undefined)
      updateOptions.visibility = options.visibility as 'public' | 'project' | 'private';

    const plan = buildUpdatePlan(updateOptions);

    if (!plan) {
      stderr.write(
        'No fields to update. Use --title, --description, --due, --start, --priority, --state, or --visibility.\n',
      );
      process.exitCode = 1;
      return;
    }

    const client = createFlowClient() as unknown as SdkClientLike;
    const { data, error } = await executeUpdate(client, id, plan);

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Failed to update task';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = 1;
      return;
    }

    const latest = data as
      | {
          id: string;
          title: string;
          derivedState: string;
        }
      | undefined;
    if (!latest) {
      stderr.write(c`{red Error}: No update applied\n`);
      process.exitCode = 1;
      return;
    }

    stdout.write(c`{green Updated} task ${latest.id}\n`);
    stdout.write(`Title: ${latest.title}\n`);
    stdout.write(`State: ${latest.derivedState}\n`);
  });

// ---------------------------------------------------------------------------
// tnk task search <query>
// ---------------------------------------------------------------------------
task
  .command('search <query>')
  .description('Search tasks by title (case-insensitive substring match)')
  .option('-w, --workspace <workspaceId>', {
    description: 'Workspace public id (required when --project is not given)',
    type: 'string',
  })
  .option('-p, --project <projectId>', {
    description: 'Project public id (alternative to --workspace)',
    type: 'string',
  })
  .option('-l, --limit <limit>', {
    description: 'Maximum number of tasks to return',
    type: 'number',
    default: 20,
  })
  .action(async ({ args, options, stdout, stderr }) => {
    const searchOptions: SearchOptionsInput = {};
    if (options.workspace !== undefined) searchOptions.workspaceId = options.workspace as string;
    if (options.project !== undefined) searchOptions.projectId = options.project as string;
    if (options.limit !== undefined) searchOptions.limit = options.limit as number;

    const result = buildSearchQuery(args.query as string, searchOptions);

    if (result === 'empty_query') {
      stderr.write('Search query must not be empty.\n');
      process.exitCode = 1;
      return;
    }
    if (result === 'missing_scope') {
      stderr.write('Either --workspace or --project must be provided to scope the search.\n');
      process.exitCode = 1;
      return;
    }

    const client = createFlowClient() as unknown as SdkClientLike;
    const { data, error } = await executeSearch(client, result);

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Failed to search tasks';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = 1;
      return;
    }

    const payload = data as
      | {
          tasks?: Array<{
            id: string;
            title: string;
            derivedState: string;
            priority: number;
            dueOn?: string | null;
          }>;
          total?: number;
        }
      | undefined;

    const tasks = payload?.tasks;
    if (!tasks || tasks.length === 0) {
      stdout.write('No tasks found.\n');
      return;
    }

    const rows = tasks.map((t) => ({
      id: t.id.slice(0, 8),
      title: t.title.length > 50 ? `${t.title.slice(0, 47)}...` : t.title,
      state: t.derivedState,
      priority: String(t.priority),
      due: t.dueOn ?? '-',
    }));

    const output = table(rows, {
      columns: ['id', 'title', 'state', 'priority', 'due'],
      headerLabels: {
        id: 'ID',
        title: 'Title',
        state: 'State',
        priority: 'Pri',
        due: 'Due',
      },
      border: 'simple',
    });

    stdout.write(output);
    stdout.write(`\n${payload?.total ?? tasks.length} task(s) total\n`);
  });

// ---------------------------------------------------------------------------
// tnk task view <id>
// ---------------------------------------------------------------------------
task
  .command('view <id>')
  .description('View task details')
  .action(async ({ args, stdout, stderr }) => {
    const id = args.id as string;
    const client = createFlowClient();

    const { data, error } = await client.GET('/tasks/{id}', {
      params: { path: { id } },
    });

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Failed to fetch task';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = 1;
      return;
    }

    stdout.write(c`{bold ${data.title}}\n`);
    stdout.write(`ID:          ${data.id}\n`);
    stdout.write(`State:       ${data.derivedState}\n`);
    stdout.write(`Priority:    ${data.priority}\n`);
    stdout.write(`Visibility:  ${data.visibility}\n`);
    stdout.write(`Project:     ${data.projectName ?? data.projectId}\n`);
    if (data.dueOn) stdout.write(`Due:         ${data.dueOn}\n`);
    if (data.startedOn) stdout.write(`Started:     ${data.startedOn}\n`);
    if (data.description) {
      stdout.write(`\n${data.description}\n`);
    }
  });

// ---------------------------------------------------------------------------
// Start
// ---------------------------------------------------------------------------
cli.start();
