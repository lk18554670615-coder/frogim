package store

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound                = errors.New("not found")
	ErrForbidden               = errors.New("forbidden")
	ErrConflict                = errors.New("conflict")
	ErrUnsupported             = errors.New("unsupported")
	ErrInviteRequired          = errors.New("invite code required")
	ErrInviteInvalid           = errors.New("invite code invalid")
	ErrInviteDisabled          = errors.New("invite code disabled")
	ErrInviteChangeUsed        = errors.New("invite code change already used")
	ErrFriendRequired          = errors.New("active friendship required")
	ErrJoinPolicy              = errors.New("group join policy does not allow this operation")
	ErrJoinRequestExpired      = errors.New("group join request expired")
	ErrGroupMessageRateLimited = errors.New("group message rate limited")
)

type GroupMessageRateLimitError struct {
	RetryAfterSeconds int
}

func (e *GroupMessageRateLimitError) Error() string {
	return fmt.Sprintf("group message rate limited; retry after %d seconds", e.RetryAfterSeconds)
}

func (e *GroupMessageRateLimitError) Unwrap() error { return ErrGroupMessageRateLimited }
