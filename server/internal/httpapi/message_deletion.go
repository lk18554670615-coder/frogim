package httpapi

import "net/http"

func (x *API) deleteMessagesForEveryone(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ConversationID string   `json:"conversationId"`
		MessageIDs     []string `json:"messageIds"`
		Confirmed      bool     `json:"confirmed"`
	}
	if decode(r, &input) != nil || !input.Confirmed || input.ConversationID == "" || len(input.MessageIDs) == 0 || len(input.MessageIDs) > 100 {
		writeError(w, 400, "INVALID_ARGUMENT", "confirmed, conversationId and 1-100 messageIds are required")
		return
	}
	result, err := x.app.DeleteMessagesForEveryone(r.Context(), uid(r), input.ConversationID, input.MessageIDs, x.clientIP(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, 200, result)
}
