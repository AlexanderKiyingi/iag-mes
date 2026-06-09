/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_MES_API_BASE?: string;
  readonly VITE_ACCESS_TOKEN?: string;
  readonly VITE_API_PROXY?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
