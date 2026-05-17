/**
 * Snapshot tests for `tnk` help output.
 *
 * The CLI does not ship with a native `addHelpText` API; help examples
 * are layered on top of `@libraz/node-cli`'s `HelpGenerator` via
 * `src/util/help.ts`. These tests freeze the rendered help text for
 * every user-facing subcommand so that regressions in either the
 * library or our example registry are caught immediately.
 *
 * The CLI module is imported for its side effects (command + example
 * registration). It guards `cli.start()` behind a main-module check so
 * the import does not consume `process.argv`.
 */

import { describe, expect, it } from 'vitest';

import { cli } from '../src/index.js';

interface HelpGeneratorLike {
  generateCommand(commandPath: string[]): string;
}

interface CLIInternal {
  helpGenerator: HelpGeneratorLike;
}

/**
 * Render the help output for a command path the same way `--help`
 * would. Reads through the (intentionally TS-private) `helpGenerator`
 * field on the CLI instance.
 */
function help(commandPath: string[]): string {
  const internal = cli as unknown as CLIInternal;
  return internal.helpGenerator.generateCommand(commandPath);
}

const allCommands: ReadonlyArray<readonly string[]> = [
  ['auth', 'login'],
  ['auth', 'logout'],
  ['auth', 'status'],
  ['task', 'list'],
  ['task', 'create'],
  ['task', 'update'],
  ['task', 'search'],
  ['task', 'view'],
];

describe('tnk help output', () => {
  for (const path of allCommands) {
    it(`matches snapshot for \`${path.join(' ')}\``, () => {
      expect(help([...path])).toMatchSnapshot();
    });
  }

  describe('examples', () => {
    it('every subcommand exposes at least one example', () => {
      for (const path of allCommands) {
        const output = help([...path]);
        expect(output, `missing Examples block for ${path.join(' ')}`).toMatch(/\nExamples:\n/);
      }
    });

    it('example block is appended after the standard help body', () => {
      const output = help(['task', 'create']);
      const optionsIdx = output.indexOf('\nOptions:\n');
      const examplesIdx = output.indexOf('\nExamples:\n');
      expect(optionsIdx).toBeGreaterThanOrEqual(0);
      expect(examplesIdx).toBeGreaterThan(optionsIdx);
    });
  });

  describe('deprecated flags', () => {
    it('surfaces the deprecation marker for `task list --workspace`', () => {
      const output = help(['task', 'list']);
      expect(output).toMatch(/--workspace\b.*\(DEPRECATED, use --workspace-id instead\)/);
    });

    it('surfaces the deprecation marker for `task create --project`', () => {
      const output = help(['task', 'create']);
      expect(output).toMatch(/--project\b.*\(DEPRECATED, use --project-id instead\)/);
    });

    it('surfaces both deprecation markers for `task search`', () => {
      const output = help(['task', 'search']);
      expect(output).toMatch(/--workspace\b.*\(DEPRECATED, use --workspace-id instead\)/);
      expect(output).toMatch(/--project\b.*\(DEPRECATED, use --project-id instead\)/);
    });
  });
});
