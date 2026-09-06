import * as k8s from "@kubernetes/client-node";

const TENANT_GROUP = "noperator.nori-cloud.io";
const TENANT_VERSION = "v1alpha1";
const TENANT_PLURAL = "tenants";
const ARGOCD_GROUP = "argoproj.io";
const ARGOCD_VERSION = "v1alpha1";
const TENANT_LABEL = "noperator.nori-cloud.io/tenant";

const kc = new k8s.KubeConfig();
if (process.env.KUBERNETES_SERVICE_HOST) {
  kc.loadFromCluster();
} else {
  kc.loadFromDefault();
}

const coreApi = kc.makeApiClient(k8s.CoreV1Api);
const eventsApi = kc.makeApiClient(k8s.EventsV1Api);
const customApi = kc.makeApiClient(k8s.CustomObjectsApi);

export type Manifest = Record<string, unknown>;

export type Condition = {
  type: string;
  status: string;
  reason?: string;
  message?: string;
};

export type TenantSummary = {
  name: string;
  namespace: string;
  environments: string[];
  ready?: string;
  readyReason?: string;
  readyMessage?: string;
};

export type ChildResource = {
  name: string;
  namespace: string;
  manifest: Manifest;
};

export type MaskedSecret = {
  name: string;
  namespace: string;
  type: string;
  dataKeys: string[];
  manifest: Manifest;
};

export type TenantEvent = {
  type: string;
  reason: string;
  message: string;
  count?: number;
  time: string;
};

export type TenantDetail = TenantSummary & {
  manifest: Manifest;
  git: { repoURL: string; revision?: string }[];
  extensions: string[];
  imagePullSecrets: { registry: string; username?: string }[];
  appProject?: ChildResource;
  applicationSets: ChildResource[];
  namespaces: ChildResource[];
  secrets: MaskedSecret[];
  serviceAccounts: ChildResource[];
  conditions: Condition[];
  events: TenantEvent[];
};

type ObjectMeta = {
  name?: string;
  namespace?: string;
};

type RawTenant = {
  metadata?: ObjectMeta;
  spec?: {
    environments?: string[];
    git?: { repoURL?: string; revision?: string }[];
    extensions?: { name?: string }[];
    imagePullSecrets?: { registry?: string; username?: string }[];
  };
  status?: { conditions?: Condition[] };
};

function readyCondition(conditions?: Condition[]): Condition | undefined {
  return conditions?.find((c) => c.type === "Ready");
}

function summarizeTenant(raw: RawTenant): TenantSummary {
  const ready = readyCondition(raw.status?.conditions);
  return {
    name: raw.metadata?.name ?? "",
    namespace: raw.metadata?.namespace ?? "",
    environments: raw.spec?.environments ?? [],
    ready: ready?.status,
    readyReason: ready?.reason,
    readyMessage: ready?.message,
  };
}

function toChildResources(
  items: { metadata?: ObjectMeta }[],
  fallbackNamespace: string,
): ChildResource[] {
  return items.map((i) => ({
    name: i.metadata?.name ?? "",
    namespace: i.metadata?.namespace ?? fallbackNamespace,
    manifest: i as unknown as Manifest,
  }));
}

// toPlain deep-converts a Kubernetes model instance (or any value) into a plain
// JSON object, which js-yaml can serialize.
function toPlain(obj: unknown): Manifest {
  return JSON.parse(JSON.stringify(obj)) as Manifest;
}

// toIsoString normalizes a timestamp that arrives as either a Date or an
// RFC3339 string (events.k8s.io eventTime) into an ISO string.
function toIsoString(value: unknown): string {
  if (typeof value === "string") return value;
  if (value instanceof Date) return value.toISOString();
  return "";
}

// maskSecretData returns a copy of the secret with every data/stringData value
// replaced by a placeholder, so credentials never leave the server.
function maskSecretData(secret: Manifest): Manifest {
  const masked: Manifest = { ...secret };
  if (secret.data && typeof secret.data === "object") {
    masked.data = Object.fromEntries(
      Object.keys(secret.data as Record<string, unknown>).map((k) => [k, "******"]),
    );
  }
  if (secret.stringData && typeof secret.stringData === "object") {
    masked.stringData = Object.fromEntries(
      Object.keys(secret.stringData as Record<string, unknown>).map((k) => [
        k,
        "******",
      ]),
    );
  }
  return masked;
}

export async function listTenants(): Promise<TenantSummary[]> {
  const body = (await customApi.listClusterCustomObject({
    group: TENANT_GROUP,
    version: TENANT_VERSION,
    plural: TENANT_PLURAL,
  })) as { items?: RawTenant[] };

  return (body.items ?? [])
    .map(summarizeTenant)
    .sort((a, b) => a.name.localeCompare(b.name));
}

export async function getTenantDetail(name: string): Promise<TenantDetail> {
  const argocdNs = process.env.ARGOCD_NAMESPACE ?? "argocd";
  const labelSelector = `${TENANT_LABEL}=${name}`;

  const tenantList = (await customApi.listClusterCustomObject({
    group: TENANT_GROUP,
    version: TENANT_VERSION,
    plural: TENANT_PLURAL,
  })) as { items?: RawTenant[] };

  const raw = (tenantList.items ?? []).find((i) => i.metadata?.name === name);
  if (!raw) {
    throw new Error(`Tenant "${name}" not found`);
  }

  const tenantNamespace = raw.metadata?.namespace ?? "noperator";

  const [appProjects, applicationSets, namespaces, secrets, serviceAccounts, events] =
    await Promise.all([
      customApi.listNamespacedCustomObject({
        group: ARGOCD_GROUP,
        version: ARGOCD_VERSION,
        namespace: argocdNs,
        plural: "appprojects",
        labelSelector,
      }),
      customApi.listNamespacedCustomObject({
        group: ARGOCD_GROUP,
        version: ARGOCD_VERSION,
        namespace: argocdNs,
        plural: "applicationsets",
        labelSelector,
      }),
      coreApi.listNamespace({ labelSelector }),
      coreApi.listSecretForAllNamespaces({ labelSelector }),
      coreApi.listServiceAccountForAllNamespaces({ labelSelector }),
      eventsApi.listNamespacedEvent({ namespace: tenantNamespace }),
    ]);

  const appProjectItems = (appProjects as { items?: { metadata?: ObjectMeta }[] })
    .items ?? [];
  const appSetItems = (applicationSets as { items?: { metadata?: ObjectMeta }[] })
    .items ?? [];

  const appProjectsResolved = toChildResources(appProjectItems, argocdNs);
  const appSetsResolved = toChildResources(appSetItems, argocdNs);

  return {
    ...summarizeTenant(raw),
    manifest: raw as unknown as Manifest,
    git: (raw.spec?.git ?? []).map((g) => ({
      repoURL: g.repoURL ?? "",
      revision: g.revision,
    })),
    extensions: (raw.spec?.extensions ?? []).map((e) => e.name ?? ""),
    imagePullSecrets: (raw.spec?.imagePullSecrets ?? []).map((ips) => ({
      registry: ips.registry ?? "",
      username: ips.username,
    })),
    appProject: appProjectsResolved[0],
    applicationSets: appSetsResolved,
    namespaces: namespaces.items.map((i) => ({
      name: i.metadata?.name ?? "",
      namespace: i.metadata?.name ?? "",
      manifest: toPlain(i),
    })),
    secrets: secrets.items.map((i) => ({
      name: i.metadata?.name ?? "",
      namespace: i.metadata?.namespace ?? "",
      type: i.type ?? "",
      dataKeys: i.data ? Object.keys(i.data) : [],
      manifest: maskSecretData(toPlain(i)),
    })),
    serviceAccounts: serviceAccounts.items.map((i) => ({
      name: i.metadata?.name ?? "",
      namespace: i.metadata?.namespace ?? "",
      manifest: toPlain(i),
    })),
    conditions: raw.status?.conditions ?? [],
    events: (events.items ?? [])
      .filter(
        (e) =>
          e.regarding?.name === name && e.regarding?.kind === "Tenant",
      )
      .map((e) => ({
        type: e.type ?? "",
        reason: e.reason ?? "",
        message: e.note ?? "",
        count: e.series?.count,
        time: toIsoString(e.eventTime),
      }))
      .sort((a, b) => b.time.localeCompare(a.time)),
  };
}
