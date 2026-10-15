package application

import "api-pacs/module/inference/domain/entity"

// ModelDeploymentOperationSubscriberInterface provides a scoped live feed for
// one durable model deployment operation.
type ModelDeploymentOperationSubscriberInterface interface {
	SubscribeModelDeploymentOperation(
		tenantID, modelID, deploymentID string,
		buffer int,
	) (<-chan entity.InferenceModelDeploymentOperation, func())
}
