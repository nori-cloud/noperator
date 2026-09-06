"use client";

import { authClient } from "@/lib/auth-client";

export function SignInButton() {
  return (
    <button
      onClick={() => authClient.signIn.social({ provider: "github" })}
      className="rounded-md bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition hover:bg-zinc-300"
    >
      Sign in with GitHub
    </button>
  );
}
