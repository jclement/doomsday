package prune

import (
	"fmt"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/snapshot"
)

func makeSnapshots(count int, interval time.Duration) []*snapshot.Snapshot {
	now := time.Now()
	snaps := make([]*snapshot.Snapshot, count)
	for i := 0; i < count; i++ {
		snaps[i] = &snapshot.Snapshot{
			ID:   fmt.Sprintf("snap-%03d", i),
			Time: now.Add(-time.Duration(i) * interval),
		}
	}
	return snaps
}

func TestKeepLast(t *testing.T) {
	snaps := makeSnapshots(10, time.Hour)
	policy := Policy{KeepLast: 3}

	keep, forget := ApplyPolicy(snaps, policy)
	if len(keep) != 3 {
		t.Errorf("keep = %d, want 3", len(keep))
	}
	if len(forget) != 7 {
		t.Errorf("forget = %d, want 7", len(forget))
	}

	// Should keep the 3 newest
	for i, s := range keep {
		if s.ID != fmt.Sprintf("snap-%03d", i) {
			t.Errorf("keep[%d] = %s", i, s.ID)
		}
	}
}

func TestKeepDaily(t *testing.T) {
	// One snapshot per hour for 10 days
	snaps := makeSnapshots(240, time.Hour)
	policy := Policy{KeepDaily: 5}

	keep, _ := ApplyPolicy(snaps, policy)
	if len(keep) != 5 {
		t.Errorf("keep = %d, want 5", len(keep))
	}
}

func TestKeepHourly(t *testing.T) {
	// One snapshot every 15 minutes for 2 days
	snaps := makeSnapshots(192, 15*time.Minute)
	policy := Policy{KeepHourly: 10}

	keep, _ := ApplyPolicy(snaps, policy)
	if len(keep) != 10 {
		t.Errorf("keep = %d, want 10", len(keep))
	}
}

func TestKeepWithin(t *testing.T) {
	snaps := makeSnapshots(100, time.Hour)
	policy := Policy{KeepWithin: 24 * time.Hour}

	keep, _ := ApplyPolicy(snaps, policy)
	// Should keep roughly 24 (those within the last 24 hours)
	if len(keep) < 23 || len(keep) > 25 {
		t.Errorf("keep = %d, expected ~24", len(keep))
	}
}

func TestCombinedPolicy(t *testing.T) {
	// 30 snapshots, one per day
	snaps := makeSnapshots(30, 24*time.Hour)
	policy := Policy{
		KeepLast:  3,
		KeepDaily: 7,
	}

	keep, forget := ApplyPolicy(snaps, policy)
	// keep_last=3 keeps snap-000, snap-001, snap-002
	// keep_daily=7 keeps one per day for 7 days: snap-000..snap-006
	// Union = 7
	if len(keep) != 7 {
		t.Errorf("keep = %d, want 7", len(keep))
	}
	if len(forget) != 23 {
		t.Errorf("forget = %d, want 23", len(forget))
	}
}

func TestEmptySnapshots(t *testing.T) {
	keep, forget := ApplyPolicy(nil, Policy{KeepLast: 5})
	if keep != nil || forget != nil {
		t.Error("expected nil for empty input")
	}
}

func TestKeepYearlyForever(t *testing.T) {
	// Snapshots spanning 5 years
	snaps := makeSnapshots(60, 30*24*time.Hour) // ~monthly for 5 years
	policy := Policy{KeepYearly: -1}            // forever

	keep, _ := ApplyPolicy(snaps, policy)
	// Should keep at least one per year
	if len(keep) < 4 {
		t.Errorf("keep = %d, expected >= 4 years", len(keep))
	}
}

func TestAllPoliciesZero(t *testing.T) {
	snaps := makeSnapshots(5, time.Hour)
	policy := Policy{} // all zeros — safety: newest snapshot is always kept

	keep, forget := ApplyPolicy(snaps, policy)
	if len(keep) != 1 {
		t.Errorf("keep = %d, want 1 (safety: newest always kept)", len(keep))
	}
	if len(forget) != 4 {
		t.Errorf("forget = %d, want 4", len(forget))
	}
}
