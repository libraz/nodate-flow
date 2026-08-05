// The design-token exemption annotations, in one implementation.
//
// Each check owns its own marker and reads no other:
//
//   nf-token-override        the dimension scan (spacing, sizing, radius,
//                            font size)
//   nf-color-override        the hardcoded-colour scan
//
// They were one marker until it became clear what that costs. The colour
// scan read the bare word anywhere in a file as a whole-file exemption
// while the dimension scan required a reason and covered two lines, so a
// spacing exemption added to a component switched the colour scan off for
// that component's entire file: 146 files reached that state without one
// colour exemption ever being written. Unifying the scope rule shrank the
// blast radius to the annotated line and no further — but it did not
// remove it, because an exemption written about a padding value still sat
// on the same line as a colour and still silenced it. An exemption is a
// claim about one specific thing, and one marker cannot say which.
//
// The rule, identical for both markers:
//
//   - `<marker>: <reason>` exempts the line it sits on and the one after,
//     so it can trail the declaration or precede it.
//   - `<marker>-file: <reason>` exempts the whole file, for the rare
//     source whose every literal is exempt for one stated reason.
//   - A reason is required, and it has to be words. Matching "any
//     non-space after the colon" is not enough: in `/* nf-token-override:
//     */` the comment's own closing `*` satisfies that, so a marker with
//     the justification deleted would still silence the check.
//
// Placement matters, because the scope is positional: write the marker on
// its own line directly above what it exempts, or trailing the
// declaration. Never after an at-rule's opening brace — the formatter
// relocates a comment written there onto the next line, which moves the
// at-rule out of the marker's reach.

/**
 * What has to follow the colon. Kept as one source string because the
 * dimension scan lives in packages/ui and compiles with `rootDir: "."`,
 * so it cannot import this file and holds a copy; a test asserts the two
 * copies are identical.
 */
export const REASON = String.raw`[^\S\n]*(?![*/]\s*$)[A-Za-z][^\n]*[A-Za-z]`;

/**
 * Build the annotation rule for one marker name.
 *
 * @param {string} marker e.g. `nf-color-override`
 */
export function overrideRule(marker) {
  const line = new RegExp(`${marker}:${REASON}`);
  const file = new RegExp(`${marker}-file:${REASON}`);

  /**
   * Resolve a source file's exemptions.
   *
   * @param {string} src file contents
   * @returns {{wholeFile: boolean, lines: Set<number>, annotations: number[]}}
   *   `lines` holds every 1-based line an annotation covers;
   *   `annotations` holds the 1-based line each annotation sits on, so a
   *   caller can report the ones that turned out to exempt nothing.
   */
  function state(src) {
    const lines = new Set();
    const annotations = [];
    let wholeFile = false;
    const rows = src.split('\n');
    for (let i = 0; i < rows.length; i++) {
      const row = rows[i] ?? '';
      if (file.test(row)) {
        wholeFile = true;
        continue;
      }
      if (line.test(row)) {
        annotations.push(i + 1);
        lines.add(i + 1);
        lines.add(i + 2);
      }
    }
    return { wholeFile, lines, annotations };
  }

  return { line, file, state };
}
