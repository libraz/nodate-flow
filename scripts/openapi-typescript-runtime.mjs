// Resolver shim for the openapi-typescript CLI.
//
// openapi-typescript builds its output with the TypeScript compiler's
// AST factory (`ts.factory`), which only the JavaScript implementation
// of the compiler exposes; its peer range is `typescript@^5.x`. The
// workspaces here pin TypeScript 7 — the native build — where
// `ts.factory` is undefined, and the generator dies on import with
// "Cannot read properties of undefined (reading 'createKeywordTypeNode')".
//
// bun hoists `openapi-typescript` to the root `node_modules`, so it picks
// up the root TypeScript no matter which version a workspace pins, and
// neither `overrides` nor `resolutions` redirect a peer edge. This shim
// therefore resolves the bare `typescript` specifier to the 5.x copy that
// `packages/sdk` pins, and leaves every other specifier alone.
//
// Used by `make gen-sdk`:
//
//   node --import ./scripts/openapi-typescript-runtime.mjs \
//     node_modules/openapi-typescript/bin/cli.js ...

import { createRequire, registerHooks } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const sdkDir = resolve(rootDir, 'packages/sdk');
const require = createRequire(import.meta.url);

let entry;
try {
  entry = require.resolve('typescript', { paths: [sdkDir] });
} catch {
  throw new Error(
    `openapi-typescript needs a TypeScript 5.x install under ${sdkDir}. Run 'bun install'.`,
  );
}

const { version } = require(require.resolve('typescript/package.json', { paths: [sdkDir] }));
if (!version.startsWith('5.')) {
  throw new Error(
    `openapi-typescript needs typescript 5.x, but ${entry} is ${version}. ` +
      "Pin a 5.x release in packages/sdk's devDependencies; the generator " +
      'uses compiler APIs that later majors no longer ship.',
  );
}

const typescriptUrl = pathToFileURL(entry).href;

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === 'typescript') {
      return { url: typescriptUrl, shortCircuit: true };
    }
    return nextResolve(specifier, context);
  },
});
