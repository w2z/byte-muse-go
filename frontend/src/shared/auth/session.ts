import { create } from "zustand";
import type { User } from "../api/types";

type SessionState = {
  user: User | null;
  setUser: (user: User) => void;
  clear: () => void;
};

/**
 * 只记录已登录用户展示信息。
 * 会话凭证由 HttpOnly Cookie 持有，前端不保存 token。
 */
export const useSession = create<SessionState>((set) => ({
  user: null,
  setUser: (user) => set({ user }),
  clear: () => set({ user: null }),
}));
