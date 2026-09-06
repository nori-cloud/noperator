# Reconciliation Loop

How the tenant controller turns a `Tenant` CR into desired Kubernetes objects.

The controller does **not** deploy workloads. It renders the tenancy boundary
(namespaces + Argo CD primitives + secrets) and leaves workload deployment to
Argo CD.

```mermaid
flowchart TD
    A[Tenant change] --> B[Reconcile]
    B --> C{Get Tenant}
    C -- not found --> Z1[Stop]
    C -- found --> D{DeletionTimestamp set?}

    D -- no --> E{Has finalizer?}
    E -- no --> F[Add finalizer]
    F --> G
    E -- yes --> G

    G[Renderer.Render] --> H{Registry.Resolve<br/>extensions}
    H -- unknown extension --> X1[Ready=False<br/>UnknownExtension]
    H -- ok --> I{Resolve secret refs}
    I -- missing/invalid --> X2[Ready=False<br/>ReconcileError]
    I -- ok --> J[Return []client.Object]

    J --> K[for each object: apply]
    K --> L{Object exists?}
    L -- no --> M[Create]
    M --> N[event Created]
    L -- yes, Namespace --> O[Skip update]
    O --> N2[event Synced]
    L -- yes, other --> P[Update]
    P --> N2

    N --> Q{All objects applied?}
    N2 --> Q
    Q -- no --> K
    Q -- yes --> R[Ready=True<br/>Reconciled]
    R --> S[Requeue after interval]
    S --> B

    D -- yes --> T{Has finalizer?}
    T -- no --> Z2[Stop]
    T -- yes --> U{PreserveResourcesOnDeletion?}
    U -- yes --> V[event Preserved<br/>remove finalizer]
    V --> Z3[Tenant deleted]
    U -- no --> W[finalize]
    W --> W1[Delete ApplicationSets]
    W1 --> W2[Delete AppProject]
    W2 --> W3[Delete repo Secrets]
    W3 --> W4[Delete Namespaces]
    W4 --> Y{Children remain?}
    Y -- yes --> YY[event Finalizing<br/>retry after interval]
    YY --> W
    Y -- no --> YN[remove finalizer]
    YN --> Z3
```

## Rendered objects, in order

```text
Renderer.Render(ctx, tenant) -> []client.Object

  1. Namespace          {tenant}-{env}        per environment
  2. AppProject         {tenant}              (argocd ns)
  3. ApplicationSet     {tenant}-{env}        per environment
  4. Secret             {tenant}-repo-{i}     per repo with credentials
  5. Secret             {tenant}-pull-secret  per environment (if pull creds)
  6. ServiceAccount     default               per environment (if pull creds)
```

## Where each step lives

| Step | Code |
|------|------|
| Entry / wiring | `cmd/main.go` |
| Reconcile, gates, apply, finalize | `internal/controller/tenant_controller.go` |
| Render objects | `internal/renderer/renderer.go` |
| Resolve extensions | `internal/extensions/registry.go` |

## apply()

```text
Get existing  ->  NotFound  ->  Create            (event: Created)
              ->  Namespace ->  skip update       (event: Synced)
              ->  else      ->  copy resourceVersion, Update   (event: Synced)
```

## Error gates

```text
Registry.Resolve  -> unknown extension        -> Ready=False, Reason=UnknownExtension
Render secret ref -> missing/invalid secret   -> Ready=False, Reason=ReconcileError
apply()           -> create/update failure    -> Ready=False, Reason=ReconcileError
```
