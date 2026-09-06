import Link from "next/link";
import { listTenants } from "@/lib/k8s";
import { SignOutButton } from "@/components/sign-out-button";

export const dynamic = "force-dynamic";

function StatusBadge({ ready }: { ready?: string }) {
  if (ready === "True") {
    return (
      <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
        Ready
      </span>
    );
  }
  if (ready === "False") {
    return (
      <span className="rounded-full bg-red-500/15 px-2 py-0.5 text-xs font-medium text-red-400">
        Not ready
      </span>
    );
  }
  return (
    <span className="rounded-full bg-zinc-500/15 px-2 py-0.5 text-xs font-medium text-zinc-400">
      Unknown
    </span>
  );
}

export default async function TenantsPage() {
  const tenants = await listTenants();

  return (
    <main className="mx-auto max-w-4xl px-4 py-10">
      <div className="mb-8 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Tenants</h1>
        <SignOutButton />
      </div>

      {tenants.length === 0 ? (
        <p className="text-zinc-500">No tenants found.</p>
      ) : (
        <ul className="divide-y divide-zinc-800 rounded-lg border border-zinc-800">
          {tenants.map((t) => (
            <li key={t.name}>
              <Link
                href={`/tenants/${t.name}`}
                className="flex items-center justify-between gap-4 px-4 py-3 transition hover:bg-zinc-900"
              >
                <div className="flex items-center gap-3">
                  <span className="font-medium">{t.name}</span>
                  <span className="text-xs text-zinc-500">{t.namespace}</span>
                </div>
                <div className="flex items-center gap-4">
                  <span className="text-xs text-zinc-500">
                    {t.environments.join(", ")}
                  </span>
                  <StatusBadge ready={t.ready} />
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}
