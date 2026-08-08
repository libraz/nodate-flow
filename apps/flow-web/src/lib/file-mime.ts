/**
 * Content type resolution for attachment uploads.
 *
 * `File.type` is empty whenever the browser's own table has no entry for
 * the extension — server logs, database dumps, `.bak` files and anything
 * without an extension at all arrive that way. Sending the generic
 * `application/octet-stream` for all of them loses the one hint we do
 * have (the filename), so this module fills the type in from the
 * extension first and only falls back to the generic type when nothing
 * identifies the file.
 *
 * The server runs the same fallback before deciding whether an upload is
 * allowed, and it takes a declared type at its word, so the value chosen
 * here is also the one the presigned PUT is signed with.
 */

/** What a browser reports for a file it cannot identify. */
export const GENERIC_BINARY_TYPE = 'application/octet-stream';

/**
 * Extension → MIME type for the everyday formats browsers commonly leave
 * untyped. Kept small on purpose: an extension that is absent resolves to
 * the generic type, which the server accepts as unidentified.
 */
const EXTENSION_TYPES: Record<string, string> = {
  txt: 'text/plain',
  log: 'text/plain',
  sql: 'text/plain',
  md: 'text/markdown',
  csv: 'text/csv',
  tsv: 'text/tab-separated-values',
  json: 'application/json',
  xml: 'application/xml',
  yaml: 'text/plain',
  yml: 'text/plain',
  ini: 'text/plain',
  conf: 'text/plain',
};

/**
 * Returns the content type to declare when uploading `file`.
 *
 * The browser's own verdict wins when it has one; otherwise the
 * extension decides; otherwise the file is reported as generic binary.
 */
export function resolveContentType(file: File): string {
  if (file.type) return file.type;
  const dot = file.name.lastIndexOf('.');
  if (dot > 0) {
    const ext = file.name.slice(dot + 1).toLowerCase();
    const derived = EXTENSION_TYPES[ext];
    if (derived) return derived;
  }
  return GENERIC_BINARY_TYPE;
}
