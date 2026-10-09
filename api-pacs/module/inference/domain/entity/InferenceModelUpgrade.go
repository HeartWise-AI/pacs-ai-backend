package entity

// InferenceModelUpgradeState is the externally visible state-machine state.
type InferenceModelUpgradeState string

const (
	InferenceModelUpgradeQueued      InferenceModelUpgradeState = "queued"
	InferenceModelUpgradePulling     InferenceModelUpgradeState = "pulling"
	InferenceModelUpgradeValidating  InferenceModelUpgradeState = "validating"
	InferenceModelUpgradeDraining    InferenceModelUpgradeState = "draining"
	InferenceModelUpgradeActivating  InferenceModelUpgradeState = "activating"
	InferenceModelUpgradeVerifying   InferenceModelUpgradeState = "verifying"
	InferenceModelUpgradeSucceeded   InferenceModelUpgradeState = "succeeded"
	InferenceModelUpgradeRollingBack InferenceModelUpgradeState = "rolling_back"
	InferenceModelUpgradeRolledBack  InferenceModelUpgradeState = "rolled_back"
	InferenceModelUpgradeFailed      InferenceModelUpgradeState = "failed"
	InferenceModelUpgradeCancelled   InferenceModelUpgradeState = "cancelled"
	InferenceModelUpgradeDegraded    InferenceModelUpgradeState = "degraded"
)

func (state InferenceModelUpgradeState) Terminal() bool {
	switch state {
	case InferenceModelUpgradeSucceeded,
		InferenceModelUpgradeRolledBack,
		InferenceModelUpgradeFailed,
		InferenceModelUpgradeCancelled,
		InferenceModelUpgradeDegraded:
		return true
	default:
		return false
	}
}

// InferenceModelUpgrade is the durable audit record for one requested image
// transition. It deliberately excludes registry credentials and model envs.
type InferenceModelUpgrade struct {
	ID                            string                     `firestore:"id" json:"id"`
	TenantID                      string                     `firestore:"tenant_id" json:"tenantId"`
	ModelID                       string                     `firestore:"model_id" json:"modelId"`
	ModelName                     string                     `firestore:"model_name" json:"modelName"`
	ActorUserID                   string                     `firestore:"actor_user_id" json:"actorUserId"`
	State                         InferenceModelUpgradeState `firestore:"state" json:"state"`
	RequestedImageReference       string                     `firestore:"requested_image_reference" json:"requestedImageReference"`
	ExpectedDigest                string                     `firestore:"expected_digest,omitempty" json:"expectedDigest,omitempty"`
	DrainTimeoutSeconds           int                        `firestore:"drain_timeout_seconds" json:"drainTimeoutSeconds"`
	Previous                      InferenceModelDeployment   `firestore:"previous" json:"previous"`
	PreviousAliases               []string                   `firestore:"previous_aliases,omitempty" json:"previousAliases,omitempty"`
	PreviousSupportedOutputModes  []string                   `firestore:"previous_supported_output_modes,omitempty" json:"previousSupportedOutputModes,omitempty"`
	Candidate                     *InferenceModelDeployment  `firestore:"candidate,omitempty" json:"candidate,omitempty"`
	CandidateSupportedOutputModes []string                   `firestore:"candidate_supported_output_modes,omitempty" json:"candidateSupportedOutputModes,omitempty"`
	FailureStage                  InferenceModelUpgradeState `firestore:"failure_stage,omitempty" json:"failureStage,omitempty"`
	ErrorCode                     string                     `firestore:"error_code,omitempty" json:"errorCode,omitempty"`
	ErrorMessage                  string                     `firestore:"error_message,omitempty" json:"errorMessage,omitempty"`
	RollbackOutcome               string                     `firestore:"rollback_outcome,omitempty" json:"rollbackOutcome,omitempty"`
	RollbackError                 string                     `firestore:"rollback_error,omitempty" json:"rollbackError,omitempty"`
	PendingCleanupContainerID     string                     `firestore:"pending_cleanup_container_id,omitempty" json:"pendingCleanupContainerId,omitempty"`
	CreatedAt                     int64                      `firestore:"created_at" json:"createdAt"`
	UpdatedAt                     int64                      `firestore:"updated_at" json:"updatedAt"`
	CompletedAt                   int64                      `firestore:"completed_at,omitempty" json:"completedAt,omitempty"`
}

func (*InferenceModelUpgrade) GetModelName() string { return "inference_model_upgrades" }
