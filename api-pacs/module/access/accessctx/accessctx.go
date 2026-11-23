// Package accessctx carries the effective access of the authenticated user in
// the request context. The token-session guards attach a lazy resolver, so the
// custom role is read only by routes that need it, at most once per request.
package accessctx

import (
	"context"
	"errors"
	"sync"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/access/domain/entity"
)

type ctxKey struct{}

type lazyAccess struct {
	once    sync.Once
	resolve func() (entity.Access, error)
	access  entity.Access
	err     error
}

// ErrUnavailable is returned when access cannot be determined.
var ErrUnavailable = errors.New("access unavailable")

// WithResolver attaches a resolver evaluated on first use.
func WithResolver(ctx context.Context, resolve func() (entity.Access, error)) context.Context {
	return context.WithValue(ctx, ctxKey{}, &lazyAccess{resolve: resolve})
}

// WithAccess attaches an already resolved access.
func WithAccess(ctx context.Context, access entity.Access) context.Context {
	return WithResolver(ctx, func() (entity.Access, error) { return access, nil })
}

// Get returns the access of the authenticated user. Without a resolver (unit
// tests and routes outside the token-session guards) it falls back to the
// session role alone, which never grants custom-role permissions.
func Get(ctx context.Context) (entity.Access, error) {
	if lazy, ok := ctx.Value(ctxKey{}).(*lazyAccess); ok && lazy != nil {
		lazy.once.Do(func() {
			lazy.access, lazy.err = lazy.resolve()
		})
		if lazy.err != nil {
			return entity.Access{}, lazy.err
		}
		return lazy.access, nil
	}
	role, ok := ctx.Value(iamTypes.RoleCtx).(string)
	if !ok || role == "" {
		return entity.Access{}, ErrUnavailable
	}
	return entity.ForRole(role, nil), nil
}

// Has reports whether the authenticated user holds permission. Errors deny.
func Has(ctx context.Context, permission string) bool {
	access, err := Get(ctx)
	return err == nil && access.Has(permission)
}
