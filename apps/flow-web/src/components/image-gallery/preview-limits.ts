/** Images above this size are not loaded until the user asks for them. */
export const MAX_AUTO_PREVIEW_BYTES = 10 * 1024 * 1024;
/** How often an open gallery re-requests preview URLs so they do not expire mid-session. */
export const GALLERY_REFRESH_MS = 5 * 60 * 1000;
