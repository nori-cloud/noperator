# noperator — Architecture

The operator turns a `Tenant` CR into Argo CD primitives; Argo CD then fans
those out into the tenant's workloads. The kube-apiserver is the single source
of truth (SSOT) everything flows through.

```mermaid
flowchart TB
    TenantCR["Tenant CR<br/>(noperator ns)"] -->|"watch"| TC["tenant-controller<br/>(reconcile)"]

    TC -->|"render + apply"| Argoproj
    TC -->|"render + apply"| Core

    subgraph Argoproj["argoproj.io/v1alpha1<br/>(argocd ns)"]
        AppProject["AppProject"]
        AppSet["ApplicationSet"]
    end

    subgraph Core["v1 (core)"]
        Secret["Secret (repo/pull)"]
        SA["ServiceAccount"]
    end

    Core -->|"creates"| NS

    subgraph NS["Namespace nori-lab-prod"]
        Workloads["Deployment · Service<br/>Pod · ConfigMap · Secret"]
    end

    AppSet -->|"watch AppSet"| ArgoCD["Argo CD"]
    ArgoCD -->|"destination.namespace"| NS

    APISERVER["kube-apiserver (SSOT)<br/>the data store — everything lives here"]

    TC <-->|"watch + apply"| APISERVER
    ArgoCD <-->|"watch + create Apps"| APISERVER
    UI["Web UI<br/>(read-only)"] -->|"read"| APISERVER
    NS <--> APISERVER
```
