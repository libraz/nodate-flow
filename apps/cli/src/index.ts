#!/usr/bin/env node
// Entry point for the `tnk` CLI. This is a stub.

const [, , cmd] = process.argv;

if (cmd === 'version') {
  process.stdout.write('tnk 0.0.0\n');
  process.exit(0);
}

process.stdout.write('usage: tnk <command>\n  commands: version\n');
process.exit(cmd ? 1 : 0);
