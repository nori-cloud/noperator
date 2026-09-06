import Link from "next/link";
import { dump } from "js-yaml";
import { getTenantDetail, type Manifest } from "@/lib/k8s";

export const dynamic = "force-dynamic";

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-zinc-800">
      <h2 className="border-b border-zinc-800 px-4 py-2 text-sm font-medium text-zinc-300">
        {title}
      </h2>
      <div className="px-4 py-3">{children}</div>
    </section>
  );
}

function KindBadge({ kind }: { kind: string }) {
  return (
    <span className="rounded bg-zinc-800 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-zinc-400">
      {kind}
    </span>
  );
}

function ListField({
  label,
  items,
}: {
  label: string;
  items: React.ReactNode[];
}) {
  return (
    <div className="text-sm">
      <div className="text-zinc-500">{label}</div>
      {items.length === 0 ? (
        <div className="text-zinc-500">—</div>
      ) : (
        <ul className="mt-1 list-inside list-disc space-y-0.5 font-mono text-xs">
          {items.map((it, i) => (
            <li key={i}>{it}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ManifestItem({
  kind,
  name,
  sub,
  manifest,
}: {
  kind?: string;
  name: string;
  sub?: string;
  manifest: Manifest;
}) {
  const yaml = dump(manifest, { noRefs: true });
  return (
    <details className="group py-1">
      <summary className="flex cursor-pointer list-none items-center justify-between gap-4 rounded px-2 py-1 transition hover:bg-zinc-900">
        <span className="flex items-center gap-2 font-mono text-sm">
          <span className="text-zinc-600 transition-transform group-open:rotate-90">
            ▸
          </span>
          {kind ? <KindBadge kind={kind} /> : null}
          {name}
        </span>
        {sub ? <span className="text-xs text-zinc-500">{sub}</span> : null}
      </summary>
      <pre className="mt-2 overflow-x-auto rounded bg-zinc-900 p-3 text-xs leading-relaxed text-zinc-300">
        {yaml}
      </pre>
    </details>
  );
}

export default async function TenantDetailPage({
  params,
}: {
  params: Promise<{ name: string }>;
}) {
  const { name } = await params;
  const tenant = await getTenantDetail(name);

  return (
    <main className="mx-auto max-w-4xl px-4 py-10">
      <div className="mb-8 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Link href="/tenants" className="text-zinc-400 hover:text-zinc-200">
            ← Tenants
          </Link>
          <h1 className="text-2xl font-semibold">{tenant.name}</h1>
          <span className="text-xs text-zinc-500">{tenant.namespace}</span>
        </div>
        <span
          className={
            tenant.ready === "True"
              ? "text-sm text-emerald-400"
              : "text-sm text-red-400"
          }
        >
          {tenant.ready === "True" ? "Ready" : "Not ready"}
        </span>
      </div>

      <div className="grid gap-6">
        <Section title="Tenant CR">
          <div className="mb-3 space-y-3">
            <ListField label="Environments" items={tenant.environments} />
            <ListField label="Extensions" items={tenant.extensions} />
            <ListField
              label="Repos"
              items={tenant.git.map((g) => (
                <>
                  {g.repoURL}{" "}
                  <span className="text-zinc-500">({g.revision ?? "main"})</span>
                </>
              ))}
            />
            <ListField
              label="Image pulls"
              items={tenant.imagePullSecrets.map((ips) => (
                <>
                  {ips.registry}
                  {ips.username ? (
                    <span className="text-zinc-500"> ({ips.username})</span>
                  ) : null}
                </>
              ))}
            />
          </div>
          <ManifestItem
            name={`${tenant.name}.yaml`}
            sub={tenant.namespace}
            manifest={tenant.manifest}
          />
          <div className="mt-3 border-t border-zinc-800 pt-3">
            <h3 className="mb-1 text-xs font-medium uppercase tracking-wide text-zinc-500">
              Conditions
            </h3>
            {tenant.conditions.length === 0 ? (
              <p className="text-sm text-zinc-500">None</p>
            ) : (
              tenant.conditions.map((c) => (
                <div key={c.type} className="py-1">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-sm">{c.type}</span>
                    <span
                      className={
                        c.status === "True"
                          ? "text-xs text-emerald-400"
                          : "text-xs text-red-400"
                      }
                    >
                      {c.status}
                    </span>
                    {c.reason ? (
                      <span className="text-xs text-zinc-500">{c.reason}</span>
                    ) : null}
                  </div>
                  {c.message ? (
                    <p className="mt-1 text-xs text-zinc-400">{c.message}</p>
                  ) : null}
                </div>
              ))
            )}
          </div>
        </Section>

        <Section title="Generated manifests">
          {tenant.appProject ? (
            <ManifestItem
              kind="AppProject"
              name={tenant.appProject.name}
              sub={tenant.appProject.namespace}
              manifest={tenant.appProject.manifest}
            />
          ) : null}

          {tenant.applicationSets.map((a) => (
            <ManifestItem
              key={a.name}
              kind="ApplicationSet"
              name={a.name}
              sub={a.namespace}
              manifest={a.manifest}
            />
          ))}

          {tenant.namespaces.map((n) => (
            <ManifestItem
              key={n.name}
              kind="Namespace"
              name={n.name}
              manifest={n.manifest}
            />
          ))}

          {tenant.secrets.map((s) => (
            <ManifestItem
              key={`${s.namespace}/${s.name}`}
              kind="Secret"
              name={s.name}
              sub={s.type}
              manifest={s.manifest}
            />
          ))}

          {tenant.serviceAccounts.map((s) => (
            <ManifestItem
              key={`${s.namespace}/${s.name}`}
              kind="ServiceAccount"
              name={s.name}
              sub={s.namespace}
              manifest={s.manifest}
            />
          ))}
        </Section>

        <Section title="Events">
          {tenant.events.length === 0 ? (
            <p className="text-sm text-zinc-500">None</p>
          ) : (
            <ul className="divide-y divide-zinc-800">
              {tenant.events.map((e, i) => (
                <li key={i} className="py-2">
                  <div className="flex items-center gap-2">
                    <span
                      className={
                        e.type === "Warning"
                          ? "text-xs font-medium text-yellow-400"
                          : "text-xs font-medium text-emerald-400"
                      }
                    >
                      {e.type}
                    </span>
                    <span className="font-mono text-sm">{e.reason}</span>
                    {e.count && e.count > 1 ? (
                      <span className="text-xs text-zinc-500">×{e.count}</span>
                    ) : null}
                    <span className="ml-auto font-mono text-xs text-zinc-500">
                      {e.time.slice(0, 19).replace("T", " ")}
                    </span>
                  </div>
                  <p className="mt-1 text-xs text-zinc-400">{e.message}</p>
                </li>
              ))}
            </ul>
          )}
        </Section>
      </div>
    </main>
  );
}
