package wukong

// These values are pinned to WuKongIMGoProto v1.2.3, the protocol module used
// by WuKongIM Server v2.2.5-20260422 (commit a888f895). Keep them explicit so
// the business service does not acquire a second server runtime dependency.
const (
	ReasonUnknown               uint8 = 0
	ReasonSuccess               uint8 = 1
	ReasonAuthFail              uint8 = 2
	ReasonSubscriberNotExist    uint8 = 3
	ReasonInBlacklist           uint8 = 4
	ReasonChannelNotExist       uint8 = 5
	ReasonPayloadDecodeError    uint8 = 9
	ReasonNotAllowSend          uint8 = 11
	ReasonNotInWhitelist        uint8 = 13
	ReasonSystemError           uint8 = 15
	ReasonChannelIDError        uint8 = 16
	ReasonBan                   uint8 = 19
	ReasonRateLimit             uint8 = 22
	ReasonNotSupportChannelType uint8 = 23
	ReasonDisband               uint8 = 24
	ReasonSendBan               uint8 = 25
)
