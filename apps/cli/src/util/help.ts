/**
 * Augments `@libraz/node-cli`'s help output with per-command example
 * blocks. The library has no native `addHelpText`-style extension
 * point, so this module monkey-patches the CLI's `HelpGenerator` to
 * append examples after the standard help text.
 */

import type { createCLI } from '@libraz/node-cli';

type Cli = ReturnType<typeof createCLI>;

interface HelpGeneratorLike {
  generateCommand(commandPath: string[]): string;
}

interface CLIInternal {
  helpGenerator: HelpGeneratorLike;
}

/**
 * Map keyed by space-joined command path (e.g. `"task create"`) to a
 * list of example invocations rendered verbatim under an "Examples:"
 * heading.
 */
export type ExamplesByPath = Record<string, readonly string[]>;

/**
 * Patches `cli` so `--help` / `tnk help <cmd>` output includes the
 * configured example block. Idempotent: calling twice replaces the
 * previous patch.
 */
export function attachExamples(cli: Cli, examples: ExamplesByPath): void {
  const internal = cli as unknown as CLIInternal;
  const generator = internal.helpGenerator;
  const original = generator.generateCommand.bind(generator);
  generator.generateCommand = (commandPath: string[]): string => {
    const base = original(commandPath);
    const key = commandPath.join(' ');
    const lines = examples[key];
    if (!lines || lines.length === 0) return base;
    const block = ['', 'Examples:', ...lines.map((l) => `  ${l}`)].join('\n');
    return `${base}\n${block}`;
  };
}
