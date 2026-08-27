package httpapi

import (
	"reflect"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/wukong"
)

func TestValidateStreamEventAllowsOnlyPublicTextContract(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		eventKey  string
		eventID   string
		payload   map[string]any
		want      map[string]any
		valid     bool
	}{
		{name: "delta", eventType: wukong.MessageEventStreamDelta, eventKey: "main", eventID: "evt-1", payload: map[string]any{"kind": "text", "delta": "hello", "ignored": true}, want: map[string]any{"kind": "text", "delta": "hello"}, valid: true},
		{name: "snapshot", eventType: wukong.MessageEventStreamSnapshot, eventKey: "main", eventID: "evt-2", payload: map[string]any{"kind": "text", "text": "complete"}, want: map[string]any{"kind": "text", "text": "complete"}, valid: true},
		{name: "close", eventType: wukong.MessageEventStreamClose, eventKey: "main", eventID: "evt-3", payload: map[string]any{"end_reason": float64(7)}, want: map[string]any{"end_reason": 7}, valid: true},
		{name: "error", eventType: wukong.MessageEventStreamError, eventKey: "main", eventID: "evt-4", payload: map[string]any{"error": " retry later "}, want: map[string]any{"error": "retry later"}, valid: true},
		{name: "cancel", eventType: wukong.MessageEventStreamCancel, eventKey: "main", eventID: "evt-5", payload: map[string]any{}, valid: true},
		{name: "finish", eventType: wukong.MessageEventStreamFinish, eventID: "evt-6", payload: map[string]any{}, valid: true},
		{name: "non text delta", eventType: wukong.MessageEventStreamDelta, eventKey: "main", eventID: "evt-7", payload: map[string]any{"kind": "tool_call", "delta": "{}"}},
		{name: "oversized delta", eventType: wukong.MessageEventStreamDelta, eventKey: "main", eventID: "evt-8", payload: map[string]any{"kind": "text", "delta": strings.Repeat("x", maxStreamDeltaText+1)}},
		{name: "caller close snapshot", eventType: wukong.MessageEventStreamClose, eventKey: "main", eventID: "evt-9", payload: map[string]any{"end_reason": 0, "snapshot": map[string]any{"kind": "text", "text": "diverges"}}},
		{name: "finish payload", eventType: wukong.MessageEventStreamFinish, eventID: "evt-10", payload: map[string]any{"unexpected": true}},
		{name: "reserved key", eventType: wukong.MessageEventStreamDelta, eventKey: "__finish__", eventID: "evt-11", payload: map[string]any{"kind": "text", "delta": "x"}},
		{name: "unknown", eventType: "stream.open", eventKey: "main", eventID: "evt-12", payload: map[string]any{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, valid := validateStreamEvent(test.eventType, test.eventKey, test.eventID, test.payload)
			if valid != test.valid || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got=%#v valid=%v want=%#v valid=%v", got, valid, test.want, test.valid)
			}
		})
	}
}
