package application

import (
	"context"

	"api-pacs/module/iam/infrastructure/service/types"
)

type LoginAbuseProtectionInterface interface {
	EvaluateLoginAttempt(ctx context.Context, data types.LoginAbuseSignals) (types.LoginProtectionDecision, error)
	RecordLoginFailure(ctx context.Context, data types.LoginAbuseSignals) (types.LoginProtectionDecision, error)
	ResetAccountFailures(ctx context.Context, data types.LoginAbuseSignals) error
}
