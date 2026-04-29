package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceCommandRepositoryCircuitBreaker circuit breaker for inference command repository
type InferenceCommandRepositoryCircuitBreaker struct {
	repository.InferenceCommandRepositoryInterface
}

var config = hystrix_config.Config{}

// DeleteInferenceModel is the decorator for the inference command repository to delete inference model
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteInferenceModel(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_inference_model", config.Settings())
	errors := hystrix.Go("delete_inference_model", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteInferenceModel(ctx, ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// DeleteInferenceIngestionJob is the decorator for the inference command repository to delete inference ingestion job
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteInferenceIngestionJob(ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_inference_ingestion_job", config.Settings())
	errors := hystrix.Go("delete_inference_ingestion_job", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteInferenceIngestionJob(ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// DeleteInferenceIngestionJobByContainerID is the decorator for the inference command repository to delete inference ingestion jobs by container ID
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteInferenceIngestionJobByContainerID(tenantID, containerID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_inference_ingestion_job_by_container_id", config.Settings())
	errors := hystrix.Go("delete_inference_ingestion_job_by_container_id", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteInferenceIngestionJobByContainerID(tenantID, containerID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// DeleteModelFeedback is the decorator for the inference command repository to delete model feedback
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteModelFeedback(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_model_feedback", config.Settings())
	errors := hystrix.Go("delete_model_feedback", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteModelFeedback(ctx, ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// DeleteModelFeedbackAnswer is the decorator for the inference command repository to delete model feedback answer
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteModelFeedbackAnswer(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_model_feedback_answer", config.Settings())
	errors := hystrix.Go("delete_model_feedback_answer", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteModelFeedbackAnswer(ctx, ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// DeleteOnboardingModelQuestionnaireAnswer is the decorator for the inference command repository to delete onboarding model questionnaire answer
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteOnboardingModelQuestionnaireAnswer(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_onboarding_model_questionnaire_answer", config.Settings())
	errors := hystrix.Go("delete_onboarding_model_questionnaire_answer", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteOnboardingModelQuestionnaireAnswer(ctx, ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// InsertModelFeedbackAnswer is the decorator for the inference command repository to insert model feedback answer
func (repository *InferenceCommandRepositoryCircuitBreaker) InsertModelFeedbackAnswer(ctx context.Context, data types.AddModelFeedbackAnswer) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_model_feedback_answer", config.Settings())
	errors := hystrix.Go("insert_model_feedback_answer", func() error {
		err := repository.InferenceCommandRepositoryInterface.InsertModelFeedbackAnswer(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// InsertInferenceModel is the decorator for the inference command repository to insert inference model
func (repository *InferenceCommandRepositoryCircuitBreaker) InsertInferenceModel(ctx context.Context, data types.AddInferenceModel) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_inference_model", config.Settings())
	errors := hystrix.Go("insert_inference_model", func() error {
		err := repository.InferenceCommandRepositoryInterface.InsertInferenceModel(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// InsertInferenceIngestionJob is the decorator for the inference command repository to insert inference ingestion job
func (repository *InferenceCommandRepositoryCircuitBreaker) InsertInferenceIngestionJob(data types.CreateInferenceIngestionJob) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_inference_ingestion_job", config.Settings())
	errors := hystrix.Go("insert_inference_ingestion_job", func() error {
		err := repository.InferenceCommandRepositoryInterface.InsertInferenceIngestionJob(data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// InsertInferenceIngestionProcessingJob is the decorator for the inference command repository to insert inference ingestion processing job
func (repository *InferenceCommandRepositoryCircuitBreaker) InsertInferenceIngestionProcessingJob(data types.AddInferenceIngestionProcessingJob) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_inference_ingestion_processing_job", config.Settings())
	errors := hystrix.Go("insert_inference_ingestion_processing_job", func() error {
		err := repository.InferenceCommandRepositoryInterface.InsertInferenceIngestionProcessingJob(data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpsertIngestionCandidate is the decorator for the inference command repository to upsert ingestion candidate
func (repository *InferenceCommandRepositoryCircuitBreaker) UpsertIngestionCandidate(data types.UpsertIngestionCandidate) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("upsert_ingestion_candidate", config.Settings())
	errors := hystrix.Go("upsert_ingestion_candidate", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpsertIngestionCandidate(data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// InsertOnboardingModelQuestionnaireAnswer is the decorator for the inference command repository to insert onboarding model questionnaire answer
func (repository *InferenceCommandRepositoryCircuitBreaker) InsertOnboardingModelQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingModelQuestionnaireAnswer) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_onboarding_model_questionnaire_answer", config.Settings())
	errors := hystrix.Go("insert_onboarding_model_questionnaire_answer", func() error {
		err := repository.InferenceCommandRepositoryInterface.InsertOnboardingModelQuestionnaireAnswer(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateInferenceModel is the decorator for the inference command repository to update inference model
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_inference_model", config.Settings())
	errors := hystrix.Go("update_inference_model", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateInferenceModel(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateInferenceIngestionJob is the decorator for the inference command repository to update an inference ingestion job
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateInferenceIngestionJob(data types.UpdateInferenceIngestionJob) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_inference_ingestion_job", config.Settings())
	errors := hystrix.Go("update_inference_ingestion_job", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJob(data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateInferenceIngestionJobStatus is the decorator for the inference command repository to update the status of an inference ingestion job
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateInferenceIngestionJobStatus(ID string, status entity.InferenceIngestionJobStatus) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_inference_ingestion_job_status", config.Settings())
	errors := hystrix.Go("update_inference_ingestion_job_status", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJobStatus(ID, status)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateInferenceIngestionJobLastExecutedAt is the decorator for the inference command repository to update last executed at of an inference ingestion job
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateInferenceIngestionJobLastExecutedAt(ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_inference_ingestion_job_last_executed_at", config.Settings())
	errors := hystrix.Go("update_inference_ingestion_job_last_executed_at", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateInferenceIngestionJobLastExecutedAt(ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateCandidateStatus is the decorator for the inference command repository to update ingestion candidate status
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateCandidateStatus(ID string, status entity.InferenceIngestionCandidateStatus) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_candidate_status", config.Settings())
	errors := hystrix.Go("update_candidate_status", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateCandidateStatus(ID, status)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// SaveCandidateOrthancJobIDs is the decorator for the inference command repository to store Orthanc job IDs
func (repository *InferenceCommandRepositoryCircuitBreaker) SaveCandidateOrthancJobIDs(ID string, orthancJobIDs []string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("save_candidate_orthanc_job_ids", config.Settings())
	errors := hystrix.Go("save_candidate_orthanc_job_ids", func() error {
		err := repository.InferenceCommandRepositoryInterface.SaveCandidateOrthancJobIDs(ID, orthancJobIDs)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateCandidateRetrievalState is the decorator for the inference command repository to store retrieval state
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateCandidateRetrievalState(data types.UpdateCandidateRetrievalState) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_candidate_retrieval_state", config.Settings())
	errors := hystrix.Go("update_candidate_retrieval_state", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateCandidateRetrievalState(data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateInferenceModelContainerID is the decorator for the inference command repository to update the container ID of an inference model
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_inference_model_container_id", config.Settings())
	errors := hystrix.Go("update_inference_model_container_id", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateInferenceModelContainerID(ctx, ID, containerID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// MarkCandidateRetrievalQueued is the decorator for the inference command repository to mark ingestion candidate retrieval queued
func (repository *InferenceCommandRepositoryCircuitBreaker) MarkCandidateRetrievalQueued(ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("mark_candidate_retrieval_queued", config.Settings())
	errors := hystrix.Go("mark_candidate_retrieval_queued", func() error {
		err := repository.InferenceCommandRepositoryInterface.MarkCandidateRetrievalQueued(ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// MarkCandidateRetrieved is the decorator for the inference command repository to mark ingestion candidate retrieved
func (repository *InferenceCommandRepositoryCircuitBreaker) MarkCandidateRetrieved(ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("mark_candidate_retrieved", config.Settings())
	errors := hystrix.Go("mark_candidate_retrieved", func() error {
		err := repository.InferenceCommandRepositoryInterface.MarkCandidateRetrieved(ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// MarkCandidateRetrievedWithContext is the decorator for the inference command repository to mark ingestion candidate retrieved with retrieval context
func (repository *InferenceCommandRepositoryCircuitBreaker) MarkCandidateRetrievedWithContext(data types.UpdateCandidateRetrievalState) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("mark_candidate_retrieved_with_context", config.Settings())
	errors := hystrix.Go("mark_candidate_retrieved_with_context", func() error {
		err := repository.InferenceCommandRepositoryInterface.MarkCandidateRetrievedWithContext(data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// MarkCandidateDisappeared is the decorator for the inference command repository to mark ingestion candidate disappeared
func (repository *InferenceCommandRepositoryCircuitBreaker) MarkCandidateDisappeared(ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("mark_candidate_disappeared", config.Settings())
	errors := hystrix.Go("mark_candidate_disappeared", func() error {
		err := repository.InferenceCommandRepositoryInterface.MarkCandidateDisappeared(ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// MarkCandidateFailed is the decorator for the inference command repository to mark ingestion candidate failed
func (repository *InferenceCommandRepositoryCircuitBreaker) MarkCandidateFailed(ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("mark_candidate_failed", config.Settings())
	errors := hystrix.Go("mark_candidate_failed", func() error {
		err := repository.InferenceCommandRepositoryInterface.MarkCandidateFailed(ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// MarkCandidateFailedWithContext is the decorator for the inference command repository to mark ingestion candidate failed with retrieval context
func (repository *InferenceCommandRepositoryCircuitBreaker) MarkCandidateFailedWithContext(data types.UpdateCandidateRetrievalState) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("mark_candidate_failed_with_context", config.Settings())
	errors := hystrix.Go("mark_candidate_failed_with_context", func() error {
		err := repository.InferenceCommandRepositoryInterface.MarkCandidateFailedWithContext(data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// IncrementCandidateMissingPolls is the decorator for the inference command repository to increment candidate missing polls
func (repository *InferenceCommandRepositoryCircuitBreaker) IncrementCandidateMissingPolls(ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("increment_candidate_missing_polls", config.Settings())
	errors := hystrix.Go("increment_candidate_missing_polls", func() error {
		err := repository.InferenceCommandRepositoryInterface.IncrementCandidateMissingPolls(ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpsertModelFeedback is the decorator for the inference command repository to upsert model feedback
func (repository *InferenceCommandRepositoryCircuitBreaker) UpsertModelFeedback(ctx context.Context, data types.UpsertModelFeedback) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("upsert_model_feedback", config.Settings())
	errors := hystrix.Go("upsert_model_feedback", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpsertModelFeedback(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}
