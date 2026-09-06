"use client";

import { authClient } from "@/lib/auth-client";

export function SignOutButton() {
  return (
    <button
      onClick={() => authClient.signOut()}
      className="text-sm text-zinc-400 transition hover:text-zinc-200"
    >
      Sign out
    </button>
  );
}
