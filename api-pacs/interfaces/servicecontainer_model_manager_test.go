package interfaces

import (
	"api-pacs/module/inference/infrastructure/modelmanager"
	"testing"
	"time"
)

func TestInferenceServicesShareProcessModelManager(t *testing.T) {
	previousKernel, previousManager := k, modelManager
	t.Cleanup(func() { k = previousKernel; modelManager = previousManager })
	k = &kernel{}
	var err error
	modelManager, err = modelmanager.New(modelmanager.Config{
		Enabled: true, BudgetMiB: 100, QueueTimeout: time.Second, OperationTimeout: time.Second,
	}, &modelmanager.ContainerBackend{})
	if err != nil {
		t.Fatal(err)
	}
	first, second := InferenceCommandServiceDI(), InferenceCommandServiceDI()
	if first.ModelManager != modelManager || second.ModelManager != modelManager {
		t.Fatal("inference service construction created separate scheduling state")
	}
	modelManager = nil
	if InferenceCommandServiceDI().ModelManager != nil {
		t.Fatal("disabled manager must not inject a scheduler")
	}
}
