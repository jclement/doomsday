// Package prune implements retention policies and crash-safe garbage collection.
package prune

import (
	"sort"
	"time"

	"github.com/jclement/doomsday/internal/snapshot"
)

// Policy defines retention rules for snapshots.
type Policy struct {
	KeepLast    int           `json:"keep_last"`
	KeepHourly  int           `json:"keep_hourly"`
	KeepDaily   int           `json:"keep_daily"`
	KeepWeekly  int           `json:"keep_weekly"`
	KeepMonthly int           `json:"keep_monthly"`
	KeepYearly  int           `json:"keep_yearly"`  // -1 = forever
	KeepWithin  time.Duration `json:"keep_within"`
}

// IsZero returns true if all policy fields are zero (no retention rules set).
func (p Policy) IsZero() bool {
	return p.KeepLast == 0 && p.KeepHourly == 0 && p.KeepDaily == 0 &&
		p.KeepWeekly == 0 && p.KeepMonthly == 0 && p.KeepYearly == 0 &&
		p.KeepWithin == 0
}

// ApplyPolicy determines which snapshots to keep and which to forget.
// Returns two lists: keep and forget. Snapshots must belong to the same backup config.
//
// SAFETY: If all policy fields are zero (no retention rules configured),
// the most recent snapshot is always kept to prevent accidental total data loss
// from a misconfigured or missing prune policy.
func ApplyPolicy(snapshots []*snapshot.Snapshot, policy Policy) (keep, forget []*snapshot.Snapshot) {
	if len(snapshots) == 0 {
		return nil, nil
	}

	// Sort by time descending (newest first)
	sorted := make([]*snapshot.Snapshot, len(snapshots))
	copy(sorted, snapshots)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Time.After(sorted[j].Time)
	})

	kept := make(map[string]bool)

	// keep_last: newest N snapshots
	if policy.KeepLast > 0 {
		for i := 0; i < policy.KeepLast && i < len(sorted); i++ {
			kept[sorted[i].ID] = true
		}
	}

	// keep_within: all snapshots within duration
	if policy.KeepWithin > 0 {
		cutoff := time.Now().Add(-policy.KeepWithin)
		for _, s := range sorted {
			if s.Time.After(cutoff) {
				kept[s.ID] = true
			}
		}
	}

	// Time-bucket policies
	type bucketPolicy struct {
		count  int
		bucket func(time.Time) string
	}

	buckets := []bucketPolicy{
		{policy.KeepHourly, func(t time.Time) string { return t.UTC().Format("2006-01-02T15") }},
		{policy.KeepDaily, func(t time.Time) string { return t.UTC().Format("2006-01-02") }},
		{policy.KeepWeekly, func(t time.Time) string {
			y, w := t.UTC().ISOWeek()
			return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, (w-1)*7).Format("2006-01-02")
		}},
		{policy.KeepMonthly, func(t time.Time) string { return t.UTC().Format("2006-01") }},
		{policy.KeepYearly, func(t time.Time) string { return t.UTC().Format("2006") }},
	}

	for _, bp := range buckets {
		if bp.count == 0 {
			continue
		}

		seen := make(map[string]bool)
		count := 0
		for _, s := range sorted {
			bucket := bp.bucket(s.Time)
			if !seen[bucket] {
				seen[bucket] = true
				kept[s.ID] = true
				count++
				if bp.count > 0 && count >= bp.count {
					break
				}
			}
		}
	}

	// SAFETY: If no retention rules matched anything (all policy fields zero
	// or no snapshots matched any bucket), always keep the most recent snapshot.
	// This prevents total data loss from misconfiguration.
	if len(kept) == 0 && len(sorted) > 0 {
		kept[sorted[0].ID] = true
	}

	// Split into keep/forget
	for _, s := range sorted {
		if kept[s.ID] {
			keep = append(keep, s)
		} else {
			forget = append(forget, s)
		}
	}

	return keep, forget
}
