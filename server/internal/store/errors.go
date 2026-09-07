package store

import "errors"

var (
	ErrNotFound           = errors.New("not found")
	ErrForbidden          = errors.New("forbidden")
	ErrConflict           = errors.New("conflict")
	ErrUnsupported        = errors.New("unsupported")
	ErrInviteRequired     = errors.New("invite code required")
	ErrInviteInvalid      = errors.New("invite code invalid")
	ErrInviteDisabled     = errors.New("invite code disabled")
	ErrInviteChangeUsed   = errors.New("invite code change already used")
	ErrFriendRequired     = errors.New("active friendship required")
	ErrJoinPolicy         = errors.New("group join policy does not allow this operation")
	ErrJoinRequestExpired = errors.New("group join request expired")
)
