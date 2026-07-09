/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_GITHUB_REPOSITORY?: string;
  readonly VITE_GITHUB_REF?: string;
  readonly VITE_PROVIDER_CATALOG_URL?: string;
  readonly VITE_DEVELOPER_CATALOG_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
