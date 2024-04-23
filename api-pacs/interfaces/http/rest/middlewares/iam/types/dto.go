package types

type ctxKey int

const (
	TenantIDCtx ctxKey = iota
	UserIDCtx
	RoleCtx
)
