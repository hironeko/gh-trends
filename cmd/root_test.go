package cmd

import (
	"testing"
	"time"
)

func TestResolveComparisonYear(t *testing.T) {
	tests := []struct {
		name      string
		year      int
		requested string
		previous  bool
		wantYear  int
		wantUse   bool
		wantErr   bool
	}{
		{name: "none", year: 2026},
		{name: "previous", year: 2026, previous: true, wantYear: 2025, wantUse: true},
		{name: "explicit", year: 2026, requested: "2024", wantYear: 2024, wantUse: true},
		{name: "conflicting flags", year: 2026, requested: "2024", previous: true, wantErr: true},
		{name: "same year", year: 2026, requested: "2026", wantErr: true},
		{name: "invalid", year: 2026, requested: "year", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotYear, gotUse, err := resolveComparisonYear(tt.year, tt.requested, tt.previous)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveComparisonYear() error = %v, wantErr %v", err, tt.wantErr)
			}
			if gotYear != tt.wantYear || gotUse != tt.wantUse {
				t.Fatalf("resolveComparisonYear() = (%d, %v), want (%d, %v)", gotYear, gotUse, tt.wantYear, tt.wantUse)
			}
		})
	}
}

func TestTrendRangeLimit(t *testing.T) {
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		year        int
		comparison  int
		hasCompare  bool
		wantMonths  int
		wantPartial bool
		wantErr     bool
	}{
		{name: "past years", year: 2025, comparison: 2024, hasCompare: true, wantMonths: 12},
		{name: "current primary year", year: 2026, comparison: 2024, hasCompare: true, wantMonths: 8, wantPartial: true},
		{name: "current comparison year", year: 2024, comparison: 2026, hasCompare: true, wantMonths: 8, wantPartial: true},
		{name: "future year", year: 2027, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			months, partial, err := trendRangeLimit(tt.year, tt.comparison, tt.hasCompare, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("trendRangeLimit() error = %v, wantErr %v", err, tt.wantErr)
			}
			if months != tt.wantMonths || partial != tt.wantPartial {
				t.Fatalf("trendRangeLimit() = (%d, %v), want (%d, %v)", months, partial, tt.wantMonths, tt.wantPartial)
			}
		})
	}
}

func TestComparableMonthEnd(t *testing.T) {
	got := comparableMonthEnd(2024, time.February, 31)
	want := time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("comparableMonthEnd() = %v, want %v", got, want)
	}
}
