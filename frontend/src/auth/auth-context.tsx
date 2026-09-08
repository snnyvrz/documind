import { createContext, useContext, useEffect, useState } from "react";
import { apiFetch } from "@/lib/api";

export type AuthUser = { subject: string; email: string };
type AuthState = { status: "loading" | "authenticated" | "unauthenticated"; user: AuthUser | null; refresh: () => Promise<void>; logout: () => Promise<void> };
const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthState["status"]>("loading");
  const [user, setUser] = useState<AuthUser | null>(null);
  const refresh = async () => {
    try {
      const response = await apiFetch("/auth/session");
      if (!response.ok) { setUser(null); setStatus("unauthenticated"); return; }
      setUser((await response.json()) as AuthUser); setStatus("authenticated");
    } catch { setUser(null); setStatus("unauthenticated"); }
  };
  const logout = async () => { await apiFetch("/auth/logout", { method: "POST" }); setUser(null); setStatus("unauthenticated"); };
  useEffect(() => { void Promise.resolve().then(refresh); }, []);
  return <AuthContext.Provider value={{ status, user, refresh, logout }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error("useAuth must be used within AuthProvider");
  return value;
}
