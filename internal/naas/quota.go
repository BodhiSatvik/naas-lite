package naas

import (
	"fmt"
	"strconv"
)

// ResourceQuota is the Kubernetes ResourceQuota that naas-lite applies to a
// namespace. It is serialized as JSON, which kubectl accepts as a manifest.
type ResourceQuota struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       QuotaSpec  `json:"spec"`
}

// ObjectMeta is the subset of Kubernetes object metadata naas-lite sets.
type ObjectMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// QuotaSpec holds the hard limits of a ResourceQuota.
type QuotaSpec struct {
	Hard map[string]string `json:"hard"`
}

// QuotaFor renders the ResourceQuota for a namespace.
func QuotaFor(ns Namespace) ResourceQuota {
	cpu := strconv.Itoa(ns.CPU)
	mem := fmt.Sprintf("%dGi", ns.MemoryGi)
	return ResourceQuota{
		APIVersion: "v1",
		Kind:       "ResourceQuota",
		Metadata: ObjectMeta{
			Name:      "naas-quota",
			Namespace: ns.Name,
			Labels: map[string]string{
				"naas.example.com/team": ns.Team,
				"naas.example.com/tier": string(ns.Tier),
			},
		},
		Spec: QuotaSpec{Hard: map[string]string{
			"requests.cpu":    cpu,
			"limits.cpu":      cpu,
			"requests.memory": mem,
			"limits.memory":   mem,
		}},
	}
}
