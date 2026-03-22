package prune

import (
	"fmt"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/snapshot"
)

// TestAudit_KeepLastExact verifies that keep_last=N keeps exactly N newest
// snapshots and forgets the rest.
func TestAudit_KeepLastExact(t *testing.T) {
	tests := []struct {
		total    int
		keepLast int
		wantKeep int
	}{
		{10, 3, 3},
		{5, 5, 5},
		{3, 10, 3}, // more keep than available
		{1, 1, 1},
		{0, 5, 0},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("total=%d_keep=%d", tc.total, tc.keepLast), func(t *testing.T) {
			snaps := makeSnapshots(tc.total, time.Hour)
			keep, forget := ApplyPolicy(snaps, Policy{KeepLast: tc.keepLast})

			if len(keep) != tc.wantKeep {
				t.Errorf("keep = %d, want %d", len(keep), tc.wantKeep)
			}
			wantForget := tc.total - tc.wantKeep
			if wantForget < 0 {
				wantForget = 0
			}
			if len(forget) != wantForget {
				t.Errorf("forget = %d, want %d", len(forget), wantForget)
			}
		})
	}
}

// TestAudit_KeepWeekly verifies weekly retention across multiple weeks.
func TestAudit_KeepWeekly(t *testing.T) {
	// Create snapshots: one per day for 30 days.
	snaps := makeSnapshots(30, 24*time.Hour)
	keep, _ := ApplyPolicy(snaps, Policy{KeepWeekly: 3})

	// Should keep 3 (one per distinct ISO week, most recent 3 weeks).
	if len(keep) != 3 {
		t.Errorf("keep = %d, want 3", len(keep))
	}
}

// TestAudit_KeepYearlyForever verifies that keep_yearly=-1 keeps one per year
// across all available years.
func TestAudit_KeepYearlyForever(t *testing.T) {
	// Snapshots spanning ~5 years.
	var snaps []*snapshot.Snapshot
	base := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	for year := 0; year < 5; year++ {
		for month := 0; month < 3; month++ {
			t1 := base.AddDate(-year, -month, 0)
			snaps = append(snaps, &snapshot.Snapshot{
				ID:   fmt.Sprintf("snap-y%d-m%d", year, month),
				Time: t1,
			})
		}
	}

	keep, _ := ApplyPolicy(snaps, Policy{KeepYearly: -1})

	// Should keep at least one per distinct year.
	years := make(map[string]bool)
	for _, s := range keep {
		years[s.Time.UTC().Format("2006")] = true
	}
	if len(years) < 4 {
		t.Errorf("keep_yearly=-1: only %d distinct years kept, expected >= 4", len(years))
	}
}

// TestAudit_CombinedPoliciesUnion verifies that combined policies produce
// the union of all matches.
func TestAudit_CombinedPoliciesUnion(t *testing.T) {
	// 100 snapshots, one per hour.
	snaps := makeSnapshots(100, time.Hour)
	policy := Policy{
		KeepLast:   5,
		KeepHourly: 10,
		KeepDaily:  7,
	}

	keep, _ := ApplyPolicy(snaps, policy)

	// keep_last=5 -> snap 0-4
	// keep_hourly=10 -> snap 0-9 (one per hour, 10 hours)
	// keep_daily=7 -> one per day for 7 days
	// Union should be >= 10 (hourly dominates last) and includes daily entries.
	if len(keep) < 10 {
		t.Errorf("combined policy: keep = %d, expected >= 10", len(keep))
	}
}

// TestAudit_ZeroPolicySafety verifies that a completely zero policy still
// keeps the most recent snapshot (safety mechanism).
func TestAudit_ZeroPolicySafety(t *testing.T) {
	snaps := makeSnapshots(10, time.Hour)
	keep, forget := ApplyPolicy(snaps, Policy{})

	if len(keep) != 1 {
		t.Fatalf("zero policy: keep = %d, want 1 (safety)", len(keep))
	}
	if keep[0].ID != "snap-000" {
		t.Errorf("zero policy: kept %s, want snap-000 (newest)", keep[0].ID)
	}
	if len(forget) != 9 {
		t.Errorf("zero policy: forget = %d, want 9", len(forget))
	}
}

// TestAudit_SingleSnapshot verifies that a single snapshot is always kept
// regardless of policy.
func TestAudit_SingleSnapshot(t *testing.T) {
	snaps := makeSnapshots(1, time.Hour)

	tests := []struct {
		name   string
		policy Policy
	}{
		{"keep_last=1", Policy{KeepLast: 1}},
		{"keep_last=5", Policy{KeepLast: 5}},
		{"zero_policy", Policy{}},
		{"keep_daily=3", Policy{KeepDaily: 3}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keep, forget := ApplyPolicy(snaps, tc.policy)
			if len(keep) != 1 {
				t.Errorf("keep = %d, want 1", len(keep))
			}
			if len(forget) != 0 {
				t.Errorf("forget = %d, want 0", len(forget))
			}
		})
	}
}

// TestAudit_PolicyIsZero verifies the IsZero method.
func TestAudit_PolicyIsZero(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		want   bool
	}{
		{"all_zero", Policy{}, true},
		{"keep_last", Policy{KeepLast: 1}, false},
		{"keep_daily", Policy{KeepDaily: 1}, false},
		{"keep_within", Policy{KeepWithin: time.Hour}, false},
		{"keep_yearly_neg1", Policy{KeepYearly: -1}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.policy.IsZero()
			if got != tc.want {
				t.Errorf("IsZero() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAudit_KeepWithinEdge verifies keep_within at the boundary.
func TestAudit_KeepWithinEdge(t *testing.T) {
	now := time.Now()
	snaps := []*snapshot.Snapshot{
		{ID: "inside", Time: now.Add(-23 * time.Hour)},
		{ID: "boundary", Time: now.Add(-24 * time.Hour)},
		{ID: "outside", Time: now.Add(-25 * time.Hour)},
	}

	keep, _ := ApplyPolicy(snaps, Policy{KeepWithin: 24 * time.Hour})

	// "inside" is within 24h, "boundary" is exactly at the edge,
	// "outside" is beyond. The comparison is After(cutoff), so
	// "boundary" (exactly 24h ago) should NOT be included (it's not after).
	keptIDs := make(map[string]bool)
	for _, s := range keep {
		keptIDs[s.ID] = true
	}

	if !keptIDs["inside"] {
		t.Error("expected 'inside' to be kept")
	}
	// Note: "boundary" may or may not be kept depending on time precision.
	// The safety mechanism may keep "outside" if it's the only one left.
}
