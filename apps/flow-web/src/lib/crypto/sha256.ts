/**
 * @brief Compute the SHA-256 digest of a {@link Blob} as a lowercase hex
 * string (64 hex chars).
 *
 * @param file Any {@link Blob} or {@link File}; the entire body is read
 *             into memory once via {@link Blob.arrayBuffer}.
 * @returns A 64-character lowercase hex digest matching the regex
 *          {@code ^[0-9a-f]{64}$}, suitable for the
 *          content-addressed attachment presign endpoints.
 *
 * Used by the attachment upload pipeline so the server can perform
 * content-addressed dedup before we ever stream bytes to object
 * storage. Backed by {@link SubtleCrypto.digest}, available in every
 * supported browser over a secure context (HTTPS / localhost).
 *
 * Memory: {@link Blob.arrayBuffer} materializes the full file in RAM.
 * That matches what the existing presigned-PUT pipeline already does
 * (the upload itself feeds the same buffer into {@code fetch}), so
 * hashing does not raise the per-upload memory ceiling.
 */
export async function sha256Hex(file: Blob): Promise<string> {
  const buf = await file.arrayBuffer();
  const hash = await crypto.subtle.digest('SHA-256', buf);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}
