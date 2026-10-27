package prefilter

import (
	"strings"
	"testing"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/types"
)

func TestDeriveStems(t *testing.T) {
	tests := []struct {
		term      string
		wantStems []string
	}{
		{"mutation", []string{"mutat"}},
		{"mutations", []string{"mutat", "mutation"}},
		{"mutating", []string{"mutat"}},
		{"mutate", []string{"mutat"}},
		{"invalidate", []string{"invalidat"}},
		{"invalidation", []string{"invalidat"}},
		{"invalidates", []string{"invalidat"}},
		{"queries", []string{"query", "quer"}},
		{"query", []string{"quer"}},
		{"caching", []string{"cach"}},
		{"cached", []string{"cach"}},
		{"cache", []string{"cach"}},
		{"routes", []string{"rout", "route"}},
		{"routing", []string{"rout"}},
		{"tokens", []string{"token"}},
		// Short terms (<4 chars) should not stem
		{"rls", nil},
		{"jwt", nil},
		{"db", nil},
		{"sql", nil},
		// Phrases with punctuation or spaces should not stem
		{": any", nil},
		{"as const", nil},
	}

	for _, tt := range tests {
		got := DeriveStems(tt.term)
		for _, w := range tt.wantStems {
			found := false
			for _, g := range got {
				if g == w {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("DeriveStems(%q) = %v; missing expected stem %q", tt.term, got, w)
			}
		}
	}
}

func TestMatches(t *testing.T) {
	tests := []struct {
		pattern string
		diff    string
		want    bool
	}{
		// Case-insensitive exact
		{"mutation", "+ export const mutation = 1;", true},
		{"MUTATION", "+ export const mutation = 1;", true},
		{"mutation", "+ export const MUTATION = 1;", true},

		// Stem matching: mutation matches authedMutatingRpc
		{"mutation", "+ export const authedMutatingRpc = createProcedure()", true},
		{"mutate", "+ function authedMutatingRpc() {}", true},
		{"invalidation", "+ invalidateQuery(cacheKey);", true},
		{"invalidate", "+ resourceInvalidationList.add(id);", true},
		{"queries", "+ const q = safeQueryClient.exec();", true},
		{"caching", "+ class CacheStore {}", true},

		// Wildcards
		{"auth*rpc", "+ export const authedMutatingRpc = 1;", true},
		{"*mutation*", "+ const runMutationTest = true;", true},

		// Short terms require exact substring (case-insensitive)
		{"rls", "+ enableRowLevelSecurity()", false},
		{"rls", "+ enable RLS on table users;", true},
		{"jwt", "+ verifyJWT(token)", true},

		// Code idioms with spaces or punctuation
		{": any", "+ const x: any = 123;", true},
		{": any", "+ const x: string = 'test';", false},
		{"as const", "+ const ACTIONS = ['a', 'b'] as const;", true},
	}

	for _, tt := range tests {
		got := Matches(tt.pattern, strings.ToLower(tt.diff))
		if got != tt.want {
			t.Errorf("Matches(%q, %q) = %v; want %v", tt.pattern, tt.diff, got, tt.want)
		}
	}
}

func TestCheck(t *testing.T) {
	a := adr.ADR{
		ID:        "0055",
		Title:     "Resource truth and mutation invalidation",
		PreFilter: []string{"mutation", "invalidate"},
	}

	// Code uses authedMutatingRpc (stem: mutat)
	diff := `
+export const setUserActive = authedMutatingRpc
+  .input(z.object({ userId: z.string(), active: z.boolean() }))
+  .mutation(async ({ input, ctx }) => {
+    await ctx.db.update(users).set({ active: input.active });
+  });
`
	res := Check(a, diff)
	if res != nil {
		t.Fatalf("expected Check to return nil (matched via stem), got %v", res)
	}

	// Diff unrelated to mutations or invalidations
	unrelatedDiff := `
+export const getUser = authedQueryRpc
+  .input(z.object({ userId: z.string() }))
+  .query(async ({ input, ctx }) => {
+    return ctx.db.select().from(users);
+  });
`
	res2 := Check(a, unrelatedDiff)
	if res2 == nil {
		t.Fatalf("expected Check to return SKIPPED for unrelated diff, got nil")
		return
	}
	if res2.Status != types.StatusSKIPPED {
		t.Errorf("status = %v, want SKIPPED", res2.Status)
	}
	if !strings.Contains(res2.Explanation, `"mutation" or "invalidate"`) {
		t.Errorf("unexpected explanation: %q", res2.Explanation)
	}
}
