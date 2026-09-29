package push

import (
	"context"
	"errors"
	"net/http"
)

type submissionKey struct{}

// WithSubmissionCheck binds a platform's live database-lock check to this
// one delivery context, never to a shared provider/client or serialized payload.
// Legacy enterprise senders have no such check and retain their old behavior.
func WithSubmissionCheck(ctx context.Context, check func(context.Context) error) context.Context {
	return context.WithValue(ctx, submissionKey{}, check)
}

func checkSubmission(ctx context.Context) error {
	if ctx.Err() != nil {
		return retryableDeliveryError(errors.New("push submission cancelled"))
	}
	if check, ok := ctx.Value(submissionKey{}).(func(context.Context) error); ok {
		if check == nil || check(ctx) != nil {
			return retryableDeliveryError(errors.New("push submission authorization unavailable"))
		}
	}
	return nil
}

// The Web Push library constructs its own request after encrypting the payload.
// Check at Do, not before encryption, and keep the library's request context.
type submissionHTTPClient struct {
	next interface {
		Do(*http.Request) (*http.Response, error)
	}
}

func (c submissionHTTPClient) Do(r *http.Request) (*http.Response, error) {
	if e := checkSubmission(r.Context()); e != nil {
		return nil, e
	}
	return c.next.Do(r)
}
