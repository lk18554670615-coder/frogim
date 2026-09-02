package model

import "time"

// HistoryAccess is a server-computed, per-membership policy. A nil sequence
// identifies legacy memberships; their second-resolution timestamps fail closed.
type HistoryAccess struct {
	Version        int64  `json:"version"`
	VisibleAll     bool   `json:"visibleAll"`
	AfterSeq       *int64 `json:"afterSeq,omitempty"`
	AfterTimestamp *int64 `json:"afterTimestamp,omitempty"`
	UnreadAfterSeq int64  `json:"unreadAfterSeq"`
}

func (h HistoryAccess) Allows(seq int64, at time.Time) bool {
	if h.VisibleAll {
		return true
	}
	if h.AfterSeq != nil {
		return seq > *h.AfterSeq
	}
	return h.AfterTimestamp != nil && at.Unix() > *h.AfterTimestamp
}
