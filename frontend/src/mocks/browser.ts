import { setupWorker } from "msw/browser";
import { handlers } from "./handlers";

/** 浏览器开发环境的 MSW worker。生产构建不会调用。 */
export const worker = setupWorker(...handlers);
