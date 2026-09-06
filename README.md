# noperator

An operator that bootstraps Argo CD resources from a `Tenant` CR.

## Layout

Monorepo:

- `tenant-controller/` — the Go operator (kubebuilder / controller-runtime).
- `charts/chart/` — the Helm chart (distributed as an OCI artifact).
- `ui/` — (planned) the web UI.

All `make` targets run from `tenant-controller/`.

## What it does

`noperator` watches `Tenant` resources (group `noperator.nori-cloud.io`) and
reconciles them into a standard set of Argo CD objects:

- a namespace per environment (`{tenant}-{env}`)
- an `AppProject` whose allowlist is `core` + enabled extensions + extras
- an `ApplicationSet` per environment (matrix: list + git directories generator)
- a per-repo `repository` credential secret (GitHub App or PAT, via `$secret:key` refs)
- an image pull `dockerconfigjson` secret wired into the default `ServiceAccount`

The resource allowlist is data-driven: the operator loads an extension registry
ConfigMap (`noperator-extension-registry`) at startup and fails closed if it is
missing or invalid.

## Build & test

```sh
cd tenant-controller
make test    # unit tests
make lint    # golangci-lint
```

## Deploy

The Helm chart is the release path. It installs the CRDs, the controller, and
the extension registry ConfigMap.

```sh
cd tenant-controller
make helm-deploy IMG=ghcr.io/nori-cloud/noperator:latest
```

Or install manually:

```sh
helm upgrade --install noperator charts/chart \
  --namespace noperator-system \
  --create-namespace \
  --set manager.image.repository=ghcr.io/nori-cloud/noperator \
  --set manager.image.tag=latest
```

Customize the extension registry via `--set` on `registry.core` and
`registry.extensions`, or edit the `noperator-extension-registry` ConfigMap
after install.

## CI/CD

- `.github/workflows/release.yml` runs on push to `main`: builds and pushes the
  controller image to GHCR, then packages and pushes the Helm chart as an OCI
  artifact (`oci://ghcr.io/nori-cloud/charts`).

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
