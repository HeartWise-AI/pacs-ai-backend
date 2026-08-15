package firebaseadmin

import "context"

type TenantIDTokenVerifierInterface interface {
	VerifyTenantIDToken(ctx context.Context, tenantID, idToken string) (string, error)
}

func (sdk *FirebaseAdminSDK) VerifyTenantIDToken(ctx context.Context, tenantID, idToken string) (string, error) {
	firebaseAuth, err := sdk.App.Auth(ctx)
	if err != nil {
		return "", err
	}
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		return "", err
	}
	token, err := tenantAuth.VerifyIDToken(ctx, idToken)
	if err != nil {
		return "", err
	}
	return token.UID, nil
}
