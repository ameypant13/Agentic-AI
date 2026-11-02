import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { Brand } from "../components/Layout";
import { Spinner } from "../components/Icons";

function safeNext(next: string | null): string {
  // Only allow same-origin relative paths.
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/login")) return "/";
  return next;
}

export default function LoginPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const next = safeNext(params.get("next"));

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState<"password" | "token" | null>(null);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent, kind: "password" | "token") => {
    e.preventDefault();
    setBusy(kind);
    setError(null);
    try {
      if (kind === "password") await api.login(email.trim(), password);
      else await api.loginWithToken(token.trim());
      // The docs are a separate static site, not a client route.
      if (next.startsWith("/docs/")) window.location.assign(next);
      else navigate(next, { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(null);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="w-full max-w-[400px]">
        <div className="mb-6 flex justify-center">
          <Brand />
        </div>
        <div className="card p-6">
          <h1 className="m-0 text-xl font-semibold">Sign in</h1>
          <p className="mb-5 mt-1 text-[13px] text-muted">Use your Control Hub account.</p>

          {error && (
            <div role="alert" className="mb-4 rounded-btn bg-crit-bg px-3 py-2 text-[13px] text-crit-fg">
              {error}
            </div>
          )}

          <form onSubmit={(e) => submit(e, "password")} className="flex flex-col gap-4">
            <div>
              <label htmlFor="email" className="label">
                Email
              </label>
              <input
                id="email"
                type="email"
                className="input"
                autoComplete="username"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </div>
            <div>
              <label htmlFor="password" className="label">
                Password
              </label>
              <input
                id="password"
                type="password"
                className="input"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            <button type="submit" className="btn btn-primary w-full" disabled={busy !== null}>
              {busy === "password" && <Spinner />}
              Sign in
            </button>
          </form>

          <details className="mt-5 border-t border-line pt-4">
            <summary className="cursor-pointer text-[13px] font-medium text-slate-700">Sign in with a control-hub token</summary>
            <form onSubmit={(e) => submit(e, "token")} className="mt-3 flex flex-col gap-3">
              <p className="m-0 text-xs text-muted">For SSO users: paste a token from an existing Control Hub session.</p>
              <div>
                <label htmlFor="token" className="label">
                  Control Hub token
                </label>
                <input
                  id="token"
                  type="password"
                  className="input font-mono"
                  autoComplete="off"
                  required
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                />
              </div>
              <button type="submit" className="btn w-full" disabled={busy !== null}>
                {busy === "token" && <Spinner />}
                Sign in with token
              </button>
            </form>
          </details>
        </div>
      </div>
    </div>
  );
}
