package entity

import "context"

type deploymentContextKey struct{}

func WithExpectedDeployment(ctx context.Context, deployment ActiveDeployment) context.Context {
	return context.WithValue(ctx, deploymentContextKey{}, deployment)
}

func ExpectedDeployment(ctx context.Context) (ActiveDeployment, bool) {
	deployment, ok := ctx.Value(deploymentContextKey{}).(ActiveDeployment)
	return deployment, ok
}

// ActiveDeployment is the internal, tenant-scoped execution contract. ContainerID
// is immutable: a queued job must never resolve a mutable Docker name again.
type ActiveDeployment struct {
	TenantID         string `json:"tenantId"`
	RegistrationID   string `json:"registrationId"`
	ModelName        string `json:"modelName"`
	ModelVersion     string `json:"modelVersion"`
	CatalogVersionID string `json:"catalogVersionId"`
	ImageDigest      string `json:"imageDigest"`
	ContainerID      string `json:"containerId"`
	LocalImageID     string `json:"localImageId"`
	State            string `json:"state"`
}

type ActiveDeploymentRegistry struct {
	SchemaVersion int                `json:"schemaVersion"`
	Revision      string             `json:"revision"`
	Models        []ActiveDeployment `json:"models"`
}
