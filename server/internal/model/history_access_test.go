package model

import (
	"testing"
	"time"
)

func TestHistoryAccess(t *testing.T) {
	seq, stamp, zero := int64(10), int64(100), int64(0)
	for _, tt := range []struct {
		name   string
		access HistoryAccess
		seq    int64
		stamp  int64
		want   bool
	}{
		{"unknown fails closed", HistoryAccess{}, 11, 101, false},
		{"initial member", HistoryAccess{AfterSeq: &zero}, 1, 99, true},
		{"at join seq", HistoryAccess{AfterSeq: &seq}, 10, 101, false},
		{"after join seq", HistoryAccess{AfterSeq: &seq}, 11, 99, true},
		{"legacy same second", HistoryAccess{AfterTimestamp: &stamp}, 11, 100, false},
		{"legacy next second", HistoryAccess{AfterTimestamp: &stamp}, 11, 101, true},
		{"opened", HistoryAccess{VisibleAll: true, AfterSeq: &seq}, 1, 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.access.Allows(tt.seq, time.Unix(tt.stamp, 999999999)); got != tt.want {
				t.Fatalf("allows=%v want=%v", got, tt.want)
			}
		})
	}
}
