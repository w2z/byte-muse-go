/// <reference types="vite/client" />

/** 构建时注入的本地版本，首次渲染即可读取，不依赖网络请求。 */
interface ImportMetaEnv {
  readonly VITE_APP_VERSION: string;
}
