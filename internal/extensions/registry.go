/*
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
*/

// Package extensions loads and resolves the extension registry ConfigMap.
//
// The registry is operator-level configuration: a ConfigMap whose
// "registry.yaml" data key maps extension names (plus the reserved "core"
// entry) to lists of {group, kind} resource references. The registry is a
// liveness-level dependency: a missing or invalid registry is an error that
// must crash the operator, never a tenant-level failure.
package extensions

import (
	"context"
	"fmt"

	noperatorv1alpha1 "github.com/nori-cloud/noperator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// DefaultConfigMapName is the default name of the extension registry ConfigMap.
	DefaultConfigMapName = "noperator-extension-registry"

	// DataKey is the ConfigMap data key holding the registry YAML.
	DataKey = "registry.yaml"

	// CoreKey is the reserved registry entry applied to every tenant.
	CoreKey = "core"
)

// Registry holds the parsed extension registry.
type Registry struct {
	core       []noperatorv1alpha1.ResourceRef
	extensions map[string][]noperatorv1alpha1.ResourceRef
}

// Load reads and parses the extension registry ConfigMap. It fails closed:
// a missing ConfigMap, missing data key, or absent/empty "core" entry returns
// an error.
func Load(ctx context.Context, c client.Client, namespace, name string) (*Registry, error) {
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, cm); err != nil {
		return nil, fmt.Errorf("load extension registry: %w", err)
	}

	data, ok := cm.Data[DataKey]
	if !ok {
		return nil, fmt.Errorf("extension registry ConfigMap %s/%s is missing data key %q", namespace, name, DataKey)
	}

	raw := map[string][]noperatorv1alpha1.ResourceRef{}
	if err := yaml.Unmarshal([]byte(data), &raw); err != nil {
		return nil, fmt.Errorf("parse extension registry: %w", err)
	}

	core, ok := raw[CoreKey]
	if !ok || len(core) == 0 {
		return nil, fmt.Errorf("extension registry ConfigMap %s/%s is missing a non-empty %q entry", namespace, name, CoreKey)
	}
	delete(raw, CoreKey)

	return &Registry{core: core, extensions: raw}, nil
}

// UnknownExtensionError indicates an extension name not present in the registry.
type UnknownExtensionError struct {
	Name string
}

func (e *UnknownExtensionError) Error() string {
	return fmt.Sprintf("unknown extension %q", e.Name)
}

// Resolve returns the tenant allowlist: the core entry plus the resources of
// each named extension, deduplicated. An unknown extension name is an error.
func (r *Registry) Resolve(enabled []noperatorv1alpha1.ExtensionRef) ([]noperatorv1alpha1.ResourceRef, error) {
	out := append([]noperatorv1alpha1.ResourceRef{}, r.core...)
	for _, e := range enabled {
		refs, ok := r.extensions[e.Name]
		if !ok {
			return nil, &UnknownExtensionError{Name: e.Name}
		}
		out = append(out, refs...)
	}
	return dedupe(out), nil
}

// Has reports whether an extension name exists in the registry.
func (r *Registry) Has(name string) bool {
	_, ok := r.extensions[name]
	return ok
}

func dedupe(refs []noperatorv1alpha1.ResourceRef) []noperatorv1alpha1.ResourceRef {
	seen := map[string]struct{}{}
	out := make([]noperatorv1alpha1.ResourceRef, 0, len(refs))
	for _, ref := range refs {
		key := ref.Group + "/" + ref.Kind
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}
