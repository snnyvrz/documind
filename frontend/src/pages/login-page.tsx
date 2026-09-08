import { useState } from "react";
import type { FormEvent } from "react";
import { ModeToggle } from "@/components/mode-toggle";
import { apiFetch } from "@/lib/api";
import { useAuth } from "@/auth/auth-context";

export function LoginPage() {
  return <AuthPage mode="login" />;
}

export function RegisterPage() {
  return <AuthPage mode="register" />;
}

function AuthPage({ mode }: { mode: "login" | "register" }) {
  const { refresh } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const isRegister = mode === "register";

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      const response = await apiFetch(`/auth/${mode}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as { error?: string };
        throw new Error(body.error || "Authentication failed");
      }
      await refresh();
    } catch (submissionError) {
      setError(submissionError instanceof Error ? submissionError.message : "Authentication failed");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="relative flex min-h-screen items-center justify-center overflow-hidden bg-background px-6 py-12">
      <div className="absolute right-6 top-5"><ModeToggle /></div>
      <div className="grid w-full max-w-5xl overflow-hidden border bg-card shadow-2xl shadow-primary/5 md:grid-cols-[1.05fr_.95fr]">
        <div className="hidden min-h-[560px] flex-col justify-between bg-primary p-10 text-primary-foreground md:flex">
          <div className="text-sm font-semibold tracking-[0.2em]">DOCUMIND</div>
          <div>
            <p className="mb-4 max-w-sm text-4xl font-semibold tracking-tight">Turn dense pages into clear answers.</p>
            <p className="max-w-sm text-primary-foreground/70">Upload once. Search the source. Ask better questions.</p>
          </div>
          <p className="text-xs text-primary-foreground/60">Private by default · Built for focused reading</p>
        </div>
        <div className="flex min-h-[560px] flex-col justify-center p-8 sm:p-12">
          <div className="mb-10">
            <p className="mb-3 text-xs font-semibold tracking-[0.2em] text-primary">{isRegister ? "START FRESH" : "WELCOME BACK"}</p>
            <h1 className="text-3xl font-semibold tracking-tight">{isRegister ? "Make your documents searchable." : "Your documents, ready when you are."}</h1>
            <p className="mt-3 max-w-md text-sm leading-6 text-muted-foreground">{isRegister ? "Create an account to keep your uploads and answers private." : "Sign in to continue to your private document workspace."}</p>
          </div>
          <form className="space-y-4" onSubmit={submit}>
            <label className="block space-y-2 text-sm font-medium">Email<input className="flex h-10 w-full rounded-md border bg-background px-3 font-normal outline-none focus:ring-2 focus:ring-primary" type="email" autoComplete="email" required value={email} onChange={(event) => setEmail(event.target.value)} /></label>
            <label className="block space-y-2 text-sm font-medium">Password<input className="flex h-10 w-full rounded-md border bg-background px-3 font-normal outline-none focus:ring-2 focus:ring-primary" type="password" autoComplete={isRegister ? "new-password" : "current-password"} minLength={8} required value={password} onChange={(event) => setPassword(event.target.value)} /></label>
            {error && <p className="text-sm text-destructive" role="alert">{error}</p>}
            <button className="inline-flex h-10 w-full items-center justify-center rounded-2xl bg-primary px-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/80 disabled:cursor-not-allowed disabled:opacity-60" disabled={submitting} type="submit">{submitting ? "Please wait..." : isRegister ? "Create account" : "Sign in"}</button>
          </form>
          <p className="mt-5 text-center text-sm text-muted-foreground">
            {isRegister ? "Already have an account? " : "New here? "}
            <a className="font-medium text-foreground underline underline-offset-4" href={isRegister ? "/login" : "/register"}>{isRegister ? "Sign in" : "Create an account"}</a>
          </p>
        </div>
      </div>
    </main>
  );
}
