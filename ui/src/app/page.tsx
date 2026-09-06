import Link from "next/link";
import { auth } from "@/lib/auth";
import { headers } from "next/headers";
import { SignInButton } from "@/components/sign-in-button";

export const dynamic = "force-dynamic";

export default async function Home() {
  const session = await auth.api.getSession({
    headers: await headers(),
  });

  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-6 px-4">
      <div className="flex flex-col items-center gap-2 text-center">
        <h1 className="text-3xl font-semibold">noperator</h1>
        <p className="text-zinc-400">Argo CD tenant overview</p>
      </div>
      {session ? (
        <Link
          href="/tenants"
          className="rounded-md bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition hover:bg-zinc-300"
        >
          View tenants
        </Link>
      ) : (
        <SignInButton />
      )}
    </main>
  );
}
