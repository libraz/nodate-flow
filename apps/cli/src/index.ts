#!/usr/bin/env node
// Entry point for the `tnk` CLI.

import { pathToFileURL } from 'node:url';

import { c, createCLI, prompt, table } from '@libraz/node-cli';

import { createAuthClient, createFlowClient } from './api.js';
import { clearCredentials, getFlowApiUrl, loadCredentials, saveCredentials } from './config.js';
import {
  buildSearchQuery,
  buildUpdatePlan,
  executeSearch,
  executeUpdate,
  type SdkClientLike,
  type SearchOptions as SearchOptionsInput,
  STATE_TRANSITIONS,
  type UpdateOptions as UpdateOptionsInput,
} from './task-builders.js';
import { assertYmd, DateValidationError } from './util/date.js';
import { EXIT_AUTH, EXIT_RUNTIME, EXIT_VALIDATION, isAuthRequiredError } from './util/exit.js';
import { optionalYmd, resolveDeprecatedFlag } from './util/flags.js';
import { attachExamples, type ExamplesByPath } from './util/help.js';

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
      process.exitCode = EXIT_AUTH;
      return;
    }

    if (data.step === 'totp_required') {
      stderr.write(c`{yellow TOTP required}. Use the web UI to complete login.\n`);
      process.exitCode = EXIT_AUTH;
      return;
    }

    if (!data.accessToken) {
      stderr.write(c`{red Error}: No access token in response\n`);
      process.exitCode = EXIT_AUTH;
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
  .option('-p, --project-id <projectId>', {
    description: 'Filter by project public id',
    type: 'string',
  })
  .option('--project <projectId>', {
    description: '(DEPRECATED, use --project-id instead) Project public id',
    type: 'string',
  })
  .option('-s, --status <status>', {
    description: 'Filter by derived state',
    type: 'string',
  })
  .option('-w, --workspace-id <workspaceId>', {
    description: 'Workspace public id',
    type: 'string',
  })
  .option('--workspace <workspaceId>', {
    description: '(DEPRECATED, use --workspace-id instead) Workspace public id',
    type: 'string',
  })
  .option('-l, --limit <limit>', {
    description: 'Maximum number of tasks to return',
    type: 'number',
    default: 25,
  })
  .action(async ({ options, stdout, stderr }) => {
    const projectId = resolveDeprecatedFlag(
      options,
      'project-id',
      'project',
      '--project-id',
      '--project',
      stderr,
    );
    const workspaceId = resolveDeprecatedFlag(
      options,
      'workspace-id',
      'workspace',
      '--workspace-id',
      '--workspace',
      stderr,
    );
    const status = options.status as string | undefined;
    const limit = options.limit as number;

    let client: ReturnType<typeof createFlowClient>;
    try {
      client = createFlowClient();
    } catch (err) {
      if (isAuthRequiredError(err)) {
        stderr.write(c`{red Error}: ${(err as Error).message}\n`);
        process.exitCode = EXIT_AUTH;
        return;
      }
      throw err;
    }

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
      process.exitCode = EXIT_RUNTIME;
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
  .option('-p, --project-id <projectId>', {
    description: 'Project public id (required)',
    type: 'string',
    required: true,
  })
  .option('--project <projectId>', {
    description: '(DEPRECATED, use --project-id instead) Project public id',
    type: 'string',
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
    const projectId = resolveDeprecatedFlag(
      options,
      'project-id',
      'project',
      '--project-id',
      '--project',
      stderr,
    );
    if (!projectId) {
      // `required: true` on `--project-id` will already reject this,
      // but if only the deprecated `--project` is given without a value
      // the resolver returns undefined; surface a usage error.
      stderr.write(c`{red Error}: --project-id is required.\n`);
      process.exitCode = EXIT_VALIDATION;
      return;
    }
    const description = options.description as string | undefined;
    let dueOn: string | undefined;
    let startOn: string | undefined;
    try {
      dueOn = optionalYmd(options.due, '--due');
      startOn = optionalYmd(options.start, '--start');
    } catch (err) {
      if (err instanceof DateValidationError) {
        stderr.write(c`{red Error}: ${err.message}\n`);
        process.exitCode = EXIT_VALIDATION;
        return;
      }
      throw err;
    }
    const priority = options.priority as number;
    const visibility = (options.visibility as 'public' | 'project' | 'private') ?? 'public';

    let client: ReturnType<typeof createFlowClient>;
    try {
      client = createFlowClient();
    } catch (err) {
      if (isAuthRequiredError(err)) {
        stderr.write(c`{red Error}: ${(err as Error).message}\n`);
        process.exitCode = EXIT_AUTH;
        return;
      }
      throw err;
    }

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
      process.exitCode = EXIT_RUNTIME;
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
    try {
      // For `update`, an empty string is a sentinel that clears the
      // date, so only validate non-empty values.
      if (typeof options.due === 'string' && options.due.length > 0) {
        updateOptions.due = assertYmd(options.due, '--due');
      } else if (options.due !== undefined) {
        updateOptions.due = options.due as string;
      }
      if (typeof options.start === 'string' && options.start.length > 0) {
        updateOptions.start = assertYmd(options.start, '--start');
      } else if (options.start !== undefined) {
        updateOptions.start = options.start as string;
      }
    } catch (err) {
      if (err instanceof DateValidationError) {
        stderr.write(c`{red Error}: ${err.message}\n`);
        process.exitCode = EXIT_VALIDATION;
        return;
      }
      throw err;
    }
    if (options.priority !== undefined) updateOptions.priority = options.priority as number;
    if (options.state !== undefined) updateOptions.state = options.state as string;
    if (options.visibility !== undefined)
      updateOptions.visibility = options.visibility as 'public' | 'project' | 'private';

    const plan = buildUpdatePlan(updateOptions);

    if (!plan) {
      stderr.write(
        'No fields to update. Use --title, --description, --due, --start, --priority, --state, or --visibility.\n',
      );
      process.exitCode = EXIT_VALIDATION;
      return;
    }

    let client: SdkClientLike;
    try {
      client = createFlowClient() as unknown as SdkClientLike;
    } catch (err) {
      if (isAuthRequiredError(err)) {
        stderr.write(c`{red Error}: ${(err as Error).message}\n`);
        process.exitCode = EXIT_AUTH;
        return;
      }
      throw err;
    }
    const { data, error } = await executeUpdate(client, id, plan);

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Failed to update task';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = EXIT_RUNTIME;
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
      process.exitCode = EXIT_RUNTIME;
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
  .option('-w, --workspace-id <workspaceId>', {
    description: 'Workspace public id (required when --project-id is not given)',
    type: 'string',
  })
  .option('--workspace <workspaceId>', {
    description: '(DEPRECATED, use --workspace-id instead) Workspace public id',
    type: 'string',
  })
  .option('-p, --project-id <projectId>', {
    description: 'Project public id (alternative to --workspace-id)',
    type: 'string',
  })
  .option('--project <projectId>', {
    description: '(DEPRECATED, use --project-id instead) Project public id',
    type: 'string',
  })
  .option('-l, --limit <limit>', {
    description: 'Maximum number of tasks to return',
    type: 'number',
    default: 20,
  })
  .action(async ({ args, options, stdout, stderr }) => {
    const workspaceId = resolveDeprecatedFlag(
      options,
      'workspace-id',
      'workspace',
      '--workspace-id',
      '--workspace',
      stderr,
    );
    const projectId = resolveDeprecatedFlag(
      options,
      'project-id',
      'project',
      '--project-id',
      '--project',
      stderr,
    );
    const searchOptions: SearchOptionsInput = {};
    if (workspaceId !== undefined) searchOptions.workspaceId = workspaceId;
    if (projectId !== undefined) searchOptions.projectId = projectId;
    if (options.limit !== undefined) searchOptions.limit = options.limit as number;

    const result = buildSearchQuery(args.query as string, searchOptions);

    if (result === 'empty_query') {
      stderr.write('Search query must not be empty.\n');
      process.exitCode = EXIT_VALIDATION;
      return;
    }
    if (result === 'missing_scope') {
      stderr.write('Either --workspace-id or --project-id must be provided to scope the search.\n');
      process.exitCode = EXIT_VALIDATION;
      return;
    }

    let client: SdkClientLike;
    try {
      client = createFlowClient() as unknown as SdkClientLike;
    } catch (err) {
      if (isAuthRequiredError(err)) {
        stderr.write(c`{red Error}: ${(err as Error).message}\n`);
        process.exitCode = EXIT_AUTH;
        return;
      }
      throw err;
    }
    const { data, error } = await executeSearch(client, result);

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Failed to search tasks';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = EXIT_RUNTIME;
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
    let client: ReturnType<typeof createFlowClient>;
    try {
      client = createFlowClient();
    } catch (err) {
      if (isAuthRequiredError(err)) {
        stderr.write(c`{red Error}: ${(err as Error).message}\n`);
        process.exitCode = EXIT_AUTH;
        return;
      }
      throw err;
    }

    const { data, error } = await client.GET('/tasks/{id}', {
      params: { path: { id } },
    });

    if (error) {
      const msg =
        typeof error === 'object' && error !== null && 'detail' in error
          ? String((error as Record<string, unknown>).detail)
          : 'Failed to fetch task';
      stderr.write(c`{red Error}: ${msg}\n`);
      process.exitCode = EXIT_RUNTIME;
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
// Help examples
// ---------------------------------------------------------------------------
const examples: ExamplesByPath = {
  'auth login': ['tnk auth login'],
  'auth logout': ['tnk auth logout'],
  'auth status': ['tnk auth status'],
  'task list': [
    'tnk task list',
    'tnk task list --project-id 9c2ad1d8-1f2c-7e1c-9a8a-44c0c9f0c1ab',
    'tnk task list --workspace-id 0190f3a6-4e6c-7d2a-94c9-aa86b1f72c11 --status in_progress --limit 50',
  ],
  'task create': [
    'tnk task create --title "Write report" --project-id 9c2ad1d8-1f2c-7e1c-9a8a-44c0c9f0c1ab',
    'tnk task create --title "Ship release" --project-id 9c2ad1d8-1f2c-7e1c-9a8a-44c0c9f0c1ab --due 2026-05-01 --priority 2',
    'tnk task create -t "Internal note" -p 9c2ad1d8-1f2c-7e1c-9a8a-44c0c9f0c1ab --visibility private',
  ],
  'task update': [
    'tnk task update 9c2ad1d8 --title "Updated title"',
    'tnk task update 9c2ad1d8 --state start',
    'tnk task update 9c2ad1d8 --due 2026-06-15 --priority 3',
    'tnk task update 9c2ad1d8 --due ""',
  ],
  'task search': [
    'tnk task search "release" --workspace-id 0190f3a6-4e6c-7d2a-94c9-aa86b1f72c11',
    'tnk task search "report" --project-id 9c2ad1d8-1f2c-7e1c-9a8a-44c0c9f0c1ab --limit 10',
  ],
  'task view': ['tnk task view 9c2ad1d8-1f2c-7e1c-9a8a-44c0c9f0c1ab'],
};
attachExamples(cli, examples);

export { cli, examples };

// ---------------------------------------------------------------------------
// Start
// ---------------------------------------------------------------------------
// Only auto-start when this module is the process entry point. Importing
// it from a test must not consume `process.argv` or open the interactive
// shell.
const entry = process.argv[1];
if (entry && import.meta.url === pathToFileURL(entry).href) {
  cli.start();
}
