import 'vitest-axe/extend-expect';
import { expect } from 'vitest';
import * as axeMatchers from 'vitest-axe/matchers';

expect.extend(axeMatchers);

// Node.js 25 exposes a partial experimental `localStorage` on globalThis that
// shadows happy-dom's implementation. Force-replace with a minimal in-memory
// polyfill so tests observe happy-dom-like behavior across Node versions.
const store = new Map<string, string>();
const polyfill: Storage = {
  get length() {
    return store.size;
  },
  clear() {
    store.clear();
  },
  getItem(key) {
    return store.get(key) ?? null;
  },
  key(index) {
    return Array.from(store.keys())[index] ?? null;
  },
  removeItem(key) {
    store.delete(key);
  },
  setItem(key, value) {
    store.set(key, String(value));
  },
};
Object.defineProperty(globalThis, 'localStorage', {
  value: polyfill,
  writable: true,
  configurable: true,
});
