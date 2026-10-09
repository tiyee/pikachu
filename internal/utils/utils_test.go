package utils

import (
	"regexp"
	"testing"
)

func TestTableRegexMatchesExactDatabaseAndTable(t *testing.T) {
	re := regexp.MustCompile(EscapeRegexForTable("prod+db", "user.orders"))
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"prod+db.user.orders", true}, {"backup_prod+db.user.orders", false}, {"prod+db.user.orders_old", false}, {"prodXdb.userXorders", false},
	} {
		if got := re.MatchString(tc.name); got != tc.want {
			t.Errorf("%q matched=%v", tc.name, got)
		}
	}
}

func TestIdentifierAndCallbackURL(t *testing.T) {
	if got := EnsureQuoted("a`b"); got != "`a``b`" {
		t.Fatalf("unsafe quoted identifier %q", got)
	}
	if got := BuildCallbackURL("https://localhost/api/", "/webhook"); got != "https://localhost/api/webhook" {
		t.Fatal(got)
	}
	if got := BuildCallbackURL("https://localhost/api", "https://other/x"); got != "https://other/x" {
		t.Fatal(got)
	}
}
