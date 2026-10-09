package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

type InferenceModelUpgradeRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
	postgresqlTypes.PostgresSQLDBHandlerInterface
}

func (repository *InferenceModelUpgradeRepository) client(ctx context.Context) (*firestore.Client, error) {
	if repository == nil || repository.FirebaseAdminSDK == nil || repository.FirebaseAdminSDK.App == nil {
		return nil, errors.New(apiError.FirestoreError)
	}
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		return nil, errors.New(apiError.FirestoreError)
	}
	return client, nil
}

func legacyDeployment(model entity.InferenceModel) entity.InferenceModelDeployment {
	if model.Deployment != nil &&
		model.Deployment.ContainerID == model.ContainerID &&
		model.Deployment.ImageReference == model.DockerImage {
		return *model.Deployment
	}
	return entity.InferenceModelDeployment{
		ContainerID:    model.ContainerID,
		ImageReference: model.DockerImage,
	}
}

func (repository *InferenceModelUpgradeRepository) BeginInferenceModelUpgrade(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
) (entity.InferenceModelUpgrade, entity.InferenceModel, error) {
	client, err := repository.client(ctx)
	if err != nil {
		return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, err
	}
	var model entity.InferenceModel
	modelRef := client.Collection(model.GetModelName()).Doc(attempt.ModelID)
	var upgrade entity.InferenceModelUpgrade
	upgradeRef := client.Collection(upgrade.GetModelName()).Doc(attempt.ID)

	err = client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, getErr := tx.Get(modelRef)
		if getErr != nil {
			if status.Code(getErr) == codes.NotFound {
				return errors.New(apiError.MissingRecord)
			}
			return getErr
		}
		if decodeErr := snapshot.DataTo(&model); decodeErr != nil {
			return decodeErr
		}
		model.ID = snapshot.Ref.ID
		if model.TenantID != attempt.TenantID {
			return errors.New(apiError.MissingRecord)
		}
		if model.ActiveUpgradeID != "" || model.DeletionClaimID != "" {
			return errors.New(apiError.DuplicateRecord)
		}
		if model.ContainerID != "" {
			sharedQuery := client.Collection(model.GetModelName()).Where("container_id", "==", model.ContainerID)
			registrations, queryErr := tx.Documents(sharedQuery).GetAll()
			if queryErr != nil {
				return queryErr
			}
			for _, registration := range registrations {
				if registration.Ref.ID != modelRef.ID {
					return errors.New(apiError.InferenceUpgradeConflict)
				}
			}
		}
		attempt.ModelName = model.Name
		attempt.Previous = legacyDeployment(model)
		if createErr := tx.Create(upgradeRef, attempt); createErr != nil {
			return createErr
		}
		return tx.Update(modelRef, []firestore.Update{
			{Path: "active_upgrade_id", Value: attempt.ID},
			{Path: "updated_at", Value: attempt.UpdatedAt},
		})
	})
	if err != nil {
		switch err.Error() {
		case apiError.MissingRecord, apiError.DuplicateRecord, apiError.InferenceUpgradeConflict:
			return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, err
		default:
			return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, fmt.Errorf("%s: %w", apiError.FirestoreError, err)
		}
	}
	return attempt, model, nil
}

// HasOtherInferenceModelRegistrations prevents a model-manager drain from
// affecting another registration that points at the same physical container.
func (repository *InferenceModelUpgradeRepository) HasOtherInferenceModelRegistrations(
	ctx context.Context,
	modelID, containerID string,
) (bool, error) {
	client, err := repository.client(ctx)
	if err != nil {
		return false, err
	}
	var model entity.InferenceModel
	registrations, err := client.Collection(model.GetModelName()).
		Where("container_id", "==", containerID).
		Documents(ctx).
		GetAll()
	if err != nil {
		return false, errors.New(apiError.FirestoreError)
	}
	for _, registration := range registrations {
		if registration.Ref.ID != modelID {
			return true, nil
		}
	}
	return false, nil
}

func (repository *InferenceModelUpgradeRepository) GetInferenceModelUpgrade(
	ctx context.Context,
	tenantID, modelID, upgradeID string,
) (entity.InferenceModelUpgrade, error) {
	client, err := repository.client(ctx)
	if err != nil {
		return entity.InferenceModelUpgrade{}, err
	}
	var upgrade entity.InferenceModelUpgrade
	snapshot, err := client.Collection(upgrade.GetModelName()).Doc(upgradeID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return upgrade, errors.New(apiError.MissingRecord)
		}
		return upgrade, errors.New(apiError.FirestoreError)
	}
	if err = snapshot.DataTo(&upgrade); err != nil {
		return entity.InferenceModelUpgrade{}, errors.New(apiError.FirestoreError)
	}
	if upgrade.TenantID != tenantID || upgrade.ModelID != modelID {
		return entity.InferenceModelUpgrade{}, errors.New(apiError.MissingRecord)
	}
	return upgrade, nil
}

func (repository *InferenceModelUpgradeRepository) GetInferenceModelForUpgrade(
	ctx context.Context,
	tenantID, modelID string,
) (entity.InferenceModel, error) {
	client, err := repository.client(ctx)
	if err != nil {
		return entity.InferenceModel{}, err
	}
	var model entity.InferenceModel
	snapshot, err := client.Collection(model.GetModelName()).Doc(modelID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return model, errors.New(apiError.MissingRecord)
		}
		return model, errors.New(apiError.FirestoreError)
	}
	if err = snapshot.DataTo(&model); err != nil {
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}
	model.ID = snapshot.Ref.ID
	if model.TenantID != tenantID {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}
	return model, nil
}

func (repository *InferenceModelUpgradeRepository) ListRecoverableInferenceModelUpgrades(
	ctx context.Context,
) ([]entity.InferenceModelUpgrade, error) {
	client, err := repository.client(ctx)
	if err != nil {
		return nil, err
	}
	var model entity.InferenceModelUpgrade
	collection := client.Collection(model.GetModelName())
	stateDocuments, err := collection.
		Where("state", "in", recoverableInferenceModelUpgradeStates()).
		Documents(ctx).
		GetAll()
	if err != nil {
		return nil, errors.New(apiError.FirestoreError)
	}
	cleanupDocuments, err := collection.
		Where("pending_cleanup_container_id", "!=", "").
		Documents(ctx).
		GetAll()
	if err != nil {
		return nil, errors.New(apiError.FirestoreError)
	}
	byID := make(map[string]entity.InferenceModelUpgrade, len(stateDocuments)+len(cleanupDocuments))
	for _, document := range append(stateDocuments, cleanupDocuments...) {
		var upgrade entity.InferenceModelUpgrade
		if err = document.DataTo(&upgrade); err != nil {
			return nil, errors.New(apiError.FirestoreError)
		}
		if upgrade.ID == "" {
			upgrade.ID = document.Ref.ID
		}
		byID[upgrade.ID] = upgrade
	}
	upgrades := make([]entity.InferenceModelUpgrade, 0, len(byID))
	for _, upgrade := range byID {
		upgrades = append(upgrades, upgrade)
	}
	return upgrades, nil
}

func recoverableInferenceModelUpgradeStates() []string {
	return []string{
		string(entity.InferenceModelUpgradeQueued),
		string(entity.InferenceModelUpgradePulling),
		string(entity.InferenceModelUpgradeValidating),
		string(entity.InferenceModelUpgradeDraining),
		string(entity.InferenceModelUpgradeActivating),
		string(entity.InferenceModelUpgradeVerifying),
		string(entity.InferenceModelUpgradeRollingBack),
		string(entity.InferenceModelUpgradeDegraded),
	}
}

func (repository *InferenceModelUpgradeRepository) SaveInferenceModelUpgrade(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
) error {
	client, err := repository.client(ctx)
	if err != nil {
		return err
	}
	var upgrade entity.InferenceModelUpgrade
	_, err = client.Collection(upgrade.GetModelName()).Doc(attempt.ID).Set(ctx, attempt)
	if err != nil {
		return errors.New(apiError.FirestoreError)
	}
	return nil
}

func (repository *InferenceModelUpgradeRepository) ActivateInferenceModelUpgrade(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
	deployment entity.InferenceModelDeployment,
	supportedOutputModes []string,
) error {
	return repository.commitUpgrade(ctx, attempt, &deployment, false, supportedOutputModes)
}

func (repository *InferenceModelUpgradeRepository) FinishInferenceModelUpgrade(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
	deployment *entity.InferenceModelDeployment,
	supportedOutputModes []string,
) error {
	return repository.commitUpgrade(ctx, attempt, deployment, true, supportedOutputModes)
}

// ActivateAndRetargetInferenceModelUpgrade holds every departing routing
// target's advisory lock across the Firestore registration switch and PostgreSQL
// redirect publication. Gateway requests therefore observe one side or the
// other, never the cross-store gap between them.
func (repository *InferenceModelUpgradeRepository) ActivateAndRetargetInferenceModelUpgrade(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
	deployment entity.InferenceModelDeployment,
	supportedOutputModes []string,
	fromContainerID string,
	aliases []string,
) error {
	if repository == nil || repository.PostgresSQLDBHandlerInterface == nil {
		return errors.New(apiError.DatabaseError)
	}
	fromContainerID = strings.TrimSpace(fromContainerID)
	lockTargets := inferenceRoutingLockTargets(fromContainerID, aliases)
	if fromContainerID == "" || len(lockTargets) == 0 {
		return errors.New(apiError.DatabaseError)
	}
	tx, err := repository.PostgresSQLDBHandlerInterface.BeginTx(ctx)
	if err != nil {
		return errors.New(apiError.DatabaseError)
	}
	defer func() { _ = tx.Rollback() }()
	if err = acquireInferenceRoutingLocks(ctx, tx, attempt.TenantID, lockTargets); err != nil {
		return errors.New(apiError.DatabaseError)
	}
	if err = repository.ActivateInferenceModelUpgrade(ctx, attempt, deployment, supportedOutputModes); err != nil {
		return err
	}
	if err = retargetInferenceRoutingTargets(ctx, tx, attempt.TenantID, lockTargets, deployment); err != nil {
		if compensationErr := repository.compensateActivationWhileLocked(ctx, attempt, deployment); compensationErr != nil {
			return compensationErr
		}
		return errors.New(apiError.DatabaseError)
	}
	if err = tx.Commit(); err != nil {
		return repository.reconcileAmbiguousRoutingCommit(ctx, attempt, deployment, lockTargets)
	}
	return nil
}

func acquireInferenceRoutingLocks(ctx context.Context, tx *sqlx.Tx, tenantID string, targets []string) error {
	for _, target := range targets {
		if _, err := tx.ExecContext(ctx,
			"SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
			tenantID+":"+target,
		); err != nil {
			return err
		}
	}
	return nil
}

func retargetInferenceRoutingTargets(
	ctx context.Context,
	tx *sqlx.Tx,
	tenantID string,
	targets []string,
	deployment entity.InferenceModelDeployment,
) error {
	for index := len(targets) - 1; index >= 0; index-- {
		if err := retargetInferenceIngestionJobsTx(
			ctx, tx, tenantID, targets[index], deployment.ContainerID, deployment.ModelVersion,
		); err != nil {
			return err
		}
	}
	return nil
}

func (repository *InferenceModelUpgradeRepository) reconcileAmbiguousRoutingCommit(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
	deployment entity.InferenceModelDeployment,
	lockTargets []string,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("routing reconciliation exhausted: %w", err)
		}
		tx, err := repository.PostgresSQLDBHandlerInterface.BeginTx(ctx)
		if err != nil {
			if waitErr := waitForRoutingRetry(ctx); waitErr != nil {
				return waitErr
			}
			continue
		}
		callCtx, cancelCall := context.WithTimeout(ctx, 10*time.Second)
		err = acquireInferenceRoutingLocks(callCtx, tx, attempt.TenantID, lockTargets)
		if err != nil {
			cancelCall()
			_ = tx.Rollback()
			if waitErr := waitForRoutingRetry(ctx); waitErr != nil {
				return waitErr
			}
			continue
		}
		err = retargetInferenceRoutingTargets(callCtx, tx, attempt.TenantID, lockTargets, deployment)
		cancelCall()
		if err != nil {
			_ = tx.Rollback()
			// The original commit may already have published these redirects.
			// Keep every retry monotonic toward the Firestore-selected deployment;
			// compensating here could split a committed route from registration.
			if waitErr := waitForRoutingRetry(ctx); waitErr != nil {
				return waitErr
			}
			continue
		}
		if err = tx.Commit(); err == nil {
			return nil
		}
		if waitErr := waitForRoutingRetry(ctx); waitErr != nil {
			return waitErr
		}
	}
}

func waitForRoutingRetry(ctx context.Context) error {
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("routing reconciliation exhausted: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func inferenceRoutingLockTargets(primary string, aliases []string) []string {
	primary = strings.TrimSpace(primary)
	seen := make(map[string]struct{}, len(aliases)+1)
	targets := make([]string, 0, len(aliases)+1)
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == primary {
			continue
		}
		if _, exists := seen[alias]; exists {
			continue
		}
		seen[alias] = struct{}{}
		targets = append(targets, alias)
	}
	slices.Sort(targets)
	if primary != "" {
		targets = append(targets, primary)
	}
	return targets
}

func (repository *InferenceModelUpgradeRepository) compensateActivationWhileLocked(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
	failedDeployment entity.InferenceModelDeployment,
) error {
	compensation, supportedOutputModes := activationCompensation(attempt, failedDeployment)
	// Keep the PostgreSQL transaction and its routing lock open until Firestore
	// is confirmed restored; releasing either first would expose a split route.
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("activation compensation exhausted: %w", err)
		}
		callCtx, cancelCall := context.WithTimeout(ctx, 10*time.Second)
		err := repository.ActivateInferenceModelUpgrade(callCtx, attempt, compensation, supportedOutputModes)
		cancelCall()
		if err == nil {
			return nil
		}
		if permanentActivationCompensationError(err) {
			return err
		}
		readCtx, cancelRead := context.WithTimeout(ctx, 10*time.Second)
		registered, readErr := repository.GetInferenceModelForUpgrade(
			readCtx, attempt.TenantID, attempt.ModelID,
		)
		cancelRead()
		if readErr == nil && registered.ContainerID == compensation.ContainerID && registered.ActiveUpgradeID == attempt.ID {
			return nil
		}
		if waitErr := waitForRoutingRetry(ctx); waitErr != nil {
			return waitErr
		}
	}
}

func permanentActivationCompensationError(err error) bool {
	if err == nil {
		return false
	}
	switch err.Error() {
	case apiError.InferenceUpgradeInvalid, apiError.DuplicateRecord, apiError.MissingRecord:
		return true
	default:
		return false
	}
}

func activationCompensation(
	attempt entity.InferenceModelUpgrade,
	failedDeployment entity.InferenceModelDeployment,
) (entity.InferenceModelDeployment, []string) {
	if failedDeployment.ContainerID == attempt.Previous.ContainerID && attempt.Candidate != nil {
		return *attempt.Candidate, attempt.CandidateSupportedOutputModes
	}
	return attempt.Previous, attempt.PreviousSupportedOutputModes
}

func (repository *InferenceModelUpgradeRepository) commitUpgrade(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
	deployment *entity.InferenceModelDeployment,
	clearLock bool,
	supportedOutputModes []string,
) error {
	client, err := repository.client(ctx)
	if err != nil {
		return err
	}
	var model entity.InferenceModel
	modelRef := client.Collection(model.GetModelName()).Doc(attempt.ModelID)
	var upgrade entity.InferenceModelUpgrade
	upgradeRef := client.Collection(upgrade.GetModelName()).Doc(attempt.ID)

	err = client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, getErr := tx.Get(modelRef)
		if getErr != nil {
			return getErr
		}
		if decodeErr := snapshot.DataTo(&model); decodeErr != nil {
			return decodeErr
		}
		if model.TenantID != attempt.TenantID || model.ActiveUpgradeID != attempt.ID {
			return errors.New(apiError.DuplicateRecord)
		}
		if supportedOutputModes != nil && !slices.Contains(supportedOutputModes, string(model.OutputMode)) {
			return errors.New(apiError.InferenceUpgradeInvalid)
		}
		if setErr := tx.Set(upgradeRef, attempt); setErr != nil {
			return setErr
		}
		updates := []firestore.Update{{Path: "updated_at", Value: attempt.UpdatedAt}}
		if deployment != nil {
			updates = append(updates,
				firestore.Update{Path: "container_id", Value: deployment.ContainerID},
				firestore.Update{Path: "docker_image", Value: deployment.ImageReference},
				firestore.Update{Path: "deployment", Value: deployment},
			)
		}
		if clearLock {
			updates = append(updates, firestore.Update{Path: "active_upgrade_id", Value: firestore.Delete})
		}
		return tx.Update(modelRef, updates)
	})
	if err != nil {
		if err.Error() == apiError.DuplicateRecord || err.Error() == apiError.InferenceUpgradeInvalid {
			return err
		}
		return fmt.Errorf("%s: %w", apiError.FirestoreError, err)
	}
	return nil
}

func (repository *InferenceModelUpgradeRepository) RetargetInferenceIngestionJobs(
	ctx context.Context,
	tenantID, fromContainerID, toContainerID, modelVersion string,
) error {
	if repository == nil || repository.PostgresSQLDBHandlerInterface == nil {
		return errors.New(apiError.DatabaseError)
	}
	tx, err := repository.PostgresSQLDBHandlerInterface.BeginTx(ctx)
	if err != nil {
		return errors.New(apiError.DatabaseError)
	}
	defer func() { _ = tx.Rollback() }()
	parameters := map[string]interface{}{
		"tenant_id":         tenantID,
		"from_container_id": fromContainerID,
		"to_container_id":   toContainerID,
		"model_version":     modelVersion,
		"lock_key":          tenantID + ":" + fromContainerID,
	}
	if _, err = tx.NamedExecContext(ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended(:lock_key, 0))",
		parameters,
	); err != nil {
		return errors.New(apiError.DatabaseError)
	}
	if err = retargetInferenceIngestionJobsTx(
		ctx, tx, tenantID, fromContainerID, toContainerID, modelVersion,
	); err != nil {
		return errors.New(apiError.DatabaseError)
	}
	if err = tx.Commit(); err != nil {
		return errors.New(apiError.DatabaseError)
	}
	return nil
}

func retargetInferenceIngestionJobsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	tenantID, fromContainerID, toContainerID, modelVersion string,
) error {
	parameters := map[string]interface{}{
		"tenant_id":         tenantID,
		"from_container_id": fromContainerID,
		"to_container_id":   toContainerID,
		"model_version":     modelVersion,
	}
	if _, err := tx.NamedExecContext(ctx, `DELETE FROM inference_model_container_redirects
		WHERE tenant_id = :tenant_id
			AND from_container_id = :to_container_id
			AND to_container_id = :from_container_id`, parameters); err != nil {
		return errors.New(apiError.DatabaseError)
	}
	if _, err := tx.NamedExecContext(ctx, `UPDATE inference_model_container_redirects
		SET to_container_id = :to_container_id, model_version = :model_version
		WHERE tenant_id = :tenant_id AND to_container_id = :from_container_id`, parameters); err != nil {
		return errors.New(apiError.DatabaseError)
	}
	if _, err := tx.NamedExecContext(ctx, `INSERT INTO inference_model_container_redirects (
			tenant_id, from_container_id, to_container_id, model_version
		) VALUES (:tenant_id, :from_container_id, :to_container_id, :model_version)
		ON CONFLICT (tenant_id, from_container_id) DO UPDATE SET
			to_container_id = EXCLUDED.to_container_id,
			model_version = EXCLUDED.model_version`, parameters); err != nil {
		return errors.New(apiError.DatabaseError)
	}
	statement := `UPDATE ingestion_jobs
		SET container_id = :to_container_id, model_version = :model_version, updated_at = NOW()
		WHERE tenant_id = :tenant_id AND container_id = :from_container_id`
	_, err := tx.NamedExecContext(ctx, statement, parameters)
	return err
}

// HasActiveInferenceProcessingJobs reports whether study-service work accepted
// for a deployment can still call the internal inference gateway.
func (repository *InferenceModelUpgradeRepository) HasActiveInferenceProcessingJobs(
	ctx context.Context,
	tenantID, modelName, modelVersion string,
) (bool, error) {
	if repository == nil || repository.PostgresSQLDBHandlerInterface == nil {
		return false, errors.New(apiError.DatabaseError)
	}
	var active bool
	err := repository.PostgresSQLDBHandlerInterface.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1
		FROM ingestion_processing_jobs
		WHERE tenant_id = :tenant_id
			AND (:model_name = '' OR model_name = :model_name)
			AND (:model_version = '' OR model_version = :model_version OR model_version IS NULL)
			AND status IN ('pending', 'queued', 'running')
	)`, map[string]interface{}{
		"tenant_id":     tenantID,
		"model_name":    modelName,
		"model_version": modelVersion,
	}, &active)
	if err != nil {
		return false, errors.New(apiError.DatabaseError)
	}
	return active, nil
}
