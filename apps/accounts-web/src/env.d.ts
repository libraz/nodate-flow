/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_AUTH_API_BASE_URL?: string;
  readonly VITE_FLOW_WEB_URL?: string;
  readonly VITE_NF_ALLOWED_REDIRECT_ORIGINS?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
