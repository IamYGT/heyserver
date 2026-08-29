package db

import (
	"reflect"
	"testing"
)

func TestBuildAuditWhereSupportsServerAndTextFilters(t *testing.T) {
	where, args := buildAuditWhere(AuditFilter{
		Server:         "edge-eu-1",
		UserName:       "Admin%",
		ActionContains: "disk_",
	})
	wantWhere := " WHERE LOWER(user_name) LIKE LOWER(?) ESCAPE '\\' AND LOWER(action) LIKE LOWER(?) ESCAPE '\\' AND resource = 'system' AND action LIKE 'remote\\_%' ESCAPE '\\' AND LOWER(LTRIM(details)) LIKE LOWER(?) ESCAPE '\\'"
	wantArgs := []any{"%Admin\\%%", "%disk\\_%", "edge-eu-1:%"}
	if where != wantWhere || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("where=%q args=%v, want where=%q args=%v", where, args, wantWhere, wantArgs)
	}
}

func TestBuildAuditWhereScopesLocalSystemActions(t *testing.T) {
	where, args := buildAuditWhere(AuditFilter{Server: "local"})
	want := " WHERE resource = 'system' AND action NOT LIKE 'remote\\_%' ESCAPE '\\'"
	if where != want || len(args) != 0 {
		t.Fatalf("where=%q args=%v, want %q with no args", where, args, want)
	}
}
