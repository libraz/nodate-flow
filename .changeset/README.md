# Changesets

This directory is used by [Changesets](https://github.com/changesets/changesets) to track and manage versioning and changelogs for the nodate-flow monorepo.

## Adding a changeset

When you make a change that should appear in the changelog, run:

```sh
bun changeset
```

This will prompt you to:

1. Select the packages that have changed
2. Choose the semver bump type (major / minor / patch) for each
3. Write a summary of the change

A new Markdown file will be created in this directory describing the change. Commit it alongside your code.

## Releasing

To apply pending changesets and update versions:

```sh
bun run version
```

To publish updated packages:

```sh
bun run release
```
