package naas

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func valid() Request {
	return Request{Name: "payments-dev", Team: "payments", Tier: TierSilver, CPU: 4, MemoryGi: 8}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
		field  string // empty means valid
	}{
		{"valid request", func(r *Request) {}, ""},
		{"missing name", func(r *Request) { r.Name = "" }, "name"},
		{"uppercase name", func(r *Request) { r.Name = "Payments" }, "name"},
		{"name too long", func(r *Request) { r.Name = strings.Repeat("a", 64) }, "name"},
		{"reserved kube- prefix", func(r *Request) { r.Name = "kube-system" }, "name"},
		{"reserved default", func(r *Request) { r.Name = "default" }, "name"},
		{"missing team", func(r *Request) { r.Team = "" }, "team"},
		{"team with spaces", func(r *Request) { r.Team = "pay ments" }, "team"},
		{"unknown tier", func(r *Request) { r.Tier = "platinum" }, "tier"},
		{"zero cpu", func(r *Request) { r.CPU = 0 }, "cpu"},
		{"negative memory", func(r *Request) { r.MemoryGi = -1 }, "memoryGi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid()
			tt.mutate(&r)
			err := r.Validate()
			if tt.field == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tt.field {
				t.Fatalf("want a ValidationError on %q, got %v", tt.field, err)
			}
		})
	}
}

func TestStore(t *testing.T) {
	s := NewStore()
	s.now = func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) }

	ns, err := s.Create(valid())
	if err != nil {
		t.Fatal(err)
	}
	if !ns.CreatedAt.Equal(s.now()) {
		t.Errorf("CreatedAt = %v", ns.CreatedAt)
	}
	if _, err := s.Create(valid()); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate create: want ErrExists, got %v", err)
	}
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	other := valid()
	other.Name = "analytics-dev"
	if _, err := s.Create(other); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 2 || list[0].Name != "analytics-dev" || list[1].Name != "payments-dev" {
		t.Errorf("List should be sorted by name, got %+v", list)
	}
}

func TestQuotaFor(t *testing.T) {
	q := QuotaFor(Namespace{Request: valid()})
	if q.Kind != "ResourceQuota" || q.Metadata.Namespace != "payments-dev" {
		t.Fatalf("bad object: %+v", q)
	}
	want := map[string]string{"requests.cpu": "4", "limits.cpu": "4", "requests.memory": "8Gi", "limits.memory": "8Gi"}
	for k, v := range want {
		if q.Spec.Hard[k] != v {
			t.Errorf("hard[%s] = %q, want %q", k, q.Spec.Hard[k], v)
		}
	}
	if q.Metadata.Labels["naas.example.com/tier"] != "silver" {
		t.Errorf("tier label missing: %+v", q.Metadata.Labels)
	}
}
