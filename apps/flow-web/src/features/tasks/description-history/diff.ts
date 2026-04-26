/**
 * Minimal line-level diff between two strings, used by the
 * description-history Drawer to highlight what would change if the user
 * restored a prior version.
 *
 * Algorithm is a straightforward LCS (longest-common-subsequence)
 * back-trace operating on whole lines. Output is a flat list of segments
 * tagged with `equal | added | removed`, in the order they should be
 * rendered. We deliberately don't pull in `diff-match-patch` here — the
 * extra dependency isn't worth it for a panel that only renders task
 * descriptions, which are typically short.
 */

export type DiffOp = 'equal' | 'added' | 'removed';

export interface DiffLine {
  op: DiffOp;
  text: string;
}

/**
 * Compute a line-level diff between {@link before} and {@link after}.
 *
 * The two strings are split on newlines; the result enumerates lines
 * present in either side, marking each with whether the restore would
 * leave it untouched (`equal`), introduce it (`added` — present in
 * `before` only, since restoring brings it back), or drop it
 * (`removed` — present in `after` only, since restoring removes it).
 *
 * Conceptually `before` is the candidate version, `after` is the
 * current description, and the operation labels describe the diff
 * "current -> if restored".
 */
export function diffLines(before: string, after: string): DiffLine[] {
  const a = before.length === 0 ? [] : before.split('\n');
  const b = after.length === 0 ? [] : after.split('\n');
  const m = a.length;
  const n = b.length;

  // LCS table of suffix lengths. table[i * (n+1) + j] = lcs length of a[i:] vs b[j:].
  const stride = n + 1;
  const table = new Int32Array((m + 1) * stride);
  for (let i = m - 1; i >= 0; i -= 1) {
    for (let j = n - 1; j >= 0; j -= 1) {
      if (a[i] === b[j]) {
        const diag = table[(i + 1) * stride + (j + 1)] ?? 0;
        table[i * stride + j] = diag + 1;
      } else {
        const down = table[(i + 1) * stride + j] ?? 0;
        const right = table[i * stride + (j + 1)] ?? 0;
        table[i * stride + j] = Math.max(down, right);
      }
    }
  }

  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    const aLine = a[i] ?? '';
    const bLine = b[j] ?? '';
    if (aLine === bLine) {
      out.push({ op: 'equal', text: aLine });
      i += 1;
      j += 1;
      continue;
    }
    const down = table[(i + 1) * stride + j] ?? 0;
    const right = table[i * stride + (j + 1)] ?? 0;
    if (down >= right) {
      // Line only in `before` — restoring would re-introduce it.
      out.push({ op: 'added', text: aLine });
      i += 1;
    } else {
      out.push({ op: 'removed', text: bLine });
      j += 1;
    }
  }
  while (i < m) {
    out.push({ op: 'added', text: a[i] ?? '' });
    i += 1;
  }
  while (j < n) {
    out.push({ op: 'removed', text: b[j] ?? '' });
    j += 1;
  }
  return out;
}
