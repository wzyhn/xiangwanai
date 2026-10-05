package xiangwanadmin

import "context"

// OIDCClient exchanges only the short-lived authorization code. Provider
// access and refresh tokens remain inside the adapter and are never returned
// to the application or persisted.
type OIDCClient interface {
	AuthorizationURL(state string, nonce string, pkceChallenge string) string
	Exchange(
		context.Context,
		string,
		string,
	) (VerifiedIdentity, error)
}
