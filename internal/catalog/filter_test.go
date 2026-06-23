package catalog

import (
	"testing"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/gnocchi"
)

func TestParseJSONFilter(t *testing.T) {
	t.Parallel()

	predicate, err := ParseJSONFilter([]byte(`{"and":[{"=":{"project_id":"project-a"}},{"like":{"display_name":"vm-%"}}]}`))
	if err != nil {
		t.Fatalf("parse filter: %v", err)
	}

	resource := &gnocchi.Resource{
		ID:   "instance-a",
		Type: "instance",
		Attrs: map[string]any{
			"project_id":   "project-a",
			"display_name": "vm-a",
		},
	}
	if !predicate.Match(resource) {
		t.Fatalf("expected predicate to match resource")
	}
}

func TestParseFlatFilter(t *testing.T) {
	t.Parallel()

	predicate, err := ParseFlatFilter(`project_id = "project-a" and display_name like "vm-%"`)
	if err != nil {
		t.Fatalf("parse flat filter: %v", err)
	}

	resource := &gnocchi.Resource{
		ID:   "instance-a",
		Type: "instance",
		Attrs: map[string]any{
			"project_id":   "project-a",
			"display_name": "vm-a",
		},
	}
	if !predicate.Match(resource) {
		t.Fatalf("expected predicate to match resource")
	}
}
