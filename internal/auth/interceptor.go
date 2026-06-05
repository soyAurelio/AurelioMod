// Package auth provides ConnectRPC authentication interceptors.
package auth

import (
	"context"
	"errors"
	"os"
	"strings"

	"connectrpc.com/connect"
	pasetolib "aidanwoods.dev/go-paseto"
)

// contextKey avoids collisions with other packages' context keys.
type contextKey string

// WorkspaceIDKey is the context key for the workspace_id extracted from PASETO claims.
const WorkspaceIDKey contextKey = "workspace_id"

// WorkspaceIDFromContext retrieves the workspace_id injected by the PASETO interceptor.
func WorkspaceIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(WorkspaceIDKey).(string)
	return v, ok
}

// NewPASETOInterceptor creates a ConnectRPC unary interceptor that validates
// PASETO v4 bearer tokens when PASETO_AUTH_ENABLED=true.
//
// When the feature gate is disabled (the default), all requests pass through.
// When enabled, the interceptor:
//   - Extracts "Authorization: Bearer <token>" from request headers
//   - Verifies the token using the provided Ed25519 public key
//   - Extracts the "workspace_id" claim and injects it into the context
//   - Returns codes.Unauthenticated on missing/invalid/expired tokens
func NewPASETOInterceptor(publicKey pasetolib.V4AsymmetricPublicKey) connect.Interceptor {
	interceptor := func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if os.Getenv("PASETO_AUTH_ENABLED") != "true" {
				return next(ctx, req)
			}

			authHeader := req.Header().Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("missing or malformed Authorization header"),
				)
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")

			parser := pasetolib.NewParser()
			parser.AddRule(pasetolib.NotExpired())
			parsed, err := parser.ParseV4Public(publicKey, token, nil)
			if err != nil {
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("invalid or expired token"),
				)
			}

			workspaceID, err := parsed.GetString("workspace_id")
			if err != nil {
				return nil, connect.NewError(
					connect.CodeUnauthenticated,
					errors.New("token missing workspace_id claim"),
				)
			}

			ctx = context.WithValue(ctx, WorkspaceIDKey, workspaceID)
			return next(ctx, req)
		}
	}
	return connect.UnaryInterceptorFunc(interceptor)
}
