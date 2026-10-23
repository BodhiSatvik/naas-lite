// Package naas is the Namespace-as-a-Service domain: request validation,
// the in-memory store and ResourceQuota rendering.
package naas

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Tier is a team's service tier. It decides how much quota a team may ask for.
type Tier string

const (
	TierBronze Tier = "bronze"
	TierSilver Tier = "silver"
	TierGold   Tier = "gold"
)

// Valid reports whether t is a known tier.
func (t Tier) Valid() bool {
	switch t {
	case TierBronze, TierSilver, TierGold:
		return true
	}
	return false
}

// Request is a team's request for a namespace with a quota.
type Request struct {
	Name     string `json:"name"`
	Team     string `json:"team"`
	Tier     Tier   `json:"tier"`
	CPU      int    `json:"cpu"`      // cores
	MemoryGi int    `json:"memoryGi"` // GiB
}

// Namespace is a namespace that naas-lite has accepted.
type Namespace struct {
	Request
	CreatedAt time.Time `json:"createdAt"`
}

// ValidationError reports a bad field in a request.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Validate checks the shape of a request. It does not check quota limits.
func (r Request) Validate() error {
	switch {
	case r.Name == "":
		return &ValidationError{"name", "is required"}
	case len(r.Name) > 63 || !dns1123.MatchString(r.Name):
		return &ValidationError{"name", "must be a DNS-1123 label: lowercase letters, digits and '-', 63 characters at most"}
	case r.Name == "default" || strings.HasPrefix(r.Name, "kube-"):
		return &ValidationError{"name", fmt.Sprintf("%q is reserved", r.Name)}
	case r.Team == "":
		return &ValidationError{"team", "is required"}
	case len(r.Team) > 63 || !dns1123.MatchString(r.Team):
		return &ValidationError{"team", "must be a DNS-1123 label"}
	case !r.Tier.Valid():
		return &ValidationError{"tier", fmt.Sprintf("must be bronze, silver or gold (got %q)", r.Tier)}
	case r.CPU <= 0:
		return &ValidationError{"cpu", "must be a positive number of cores"}
	case r.MemoryGi <= 0:
		return &ValidationError{"memoryGi", "must be a positive number of GiB"}
	}
	return nil
}
