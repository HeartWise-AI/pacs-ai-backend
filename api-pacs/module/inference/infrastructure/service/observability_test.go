package service

import (
	"expvar"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
)

func TestWorklistObservabilityTracksBalancedConnectionsAndOutcomes(t *testing.T) {
	activeBefore := worklistSSEConnectionsActive.Value()
	publishedBefore := expvarMapIntValue(worklistNotificationsTotal, "published")

	ObserveWorklistSSEConnectionOpened()
	require.Equal(t, activeBefore+1, worklistSSEConnectionsActive.Value())
	ObserveWorklistNotification("published")
	ObserveWorklistSSEConnectionClosed()

	require.Equal(t, activeBefore, worklistSSEConnectionsActive.Value())
	require.Equal(t, publishedBefore+1, expvarMapIntValue(worklistNotificationsTotal, "published"))
}

func TestProcessingObservabilityUsesBoundedLabelsAndTracksCallbackLag(t *testing.T) {
	unknownKey := "status=unknown,outcome=unknown"
	unknownBefore := expvarMapIntValue(studyServiceProcessingCallbacks, unknownKey)
	ObserveStudyServiceProcessingCallback("patient-like-unbounded-value", "arbitrary-error-detail")
	require.Equal(t, unknownBefore+1, expvarMapIntValue(studyServiceProcessingCallbacks, unknownKey))
	require.Nil(t, studyServiceProcessingCallbacks.Get("status=patient-like-unbounded-value,outcome=arbitrary-error-detail"))

	lagKey := "status=completed,outcome=applied"
	lagBefore := expvarMapIntValue(studyServiceCallbackLagCount, lagKey)
	ObserveStudyServiceProcessingCallbackLag("completed", "applied", time.Now().Add(-250*time.Millisecond))
	require.Equal(t, lagBefore+1, expvarMapIntValue(studyServiceCallbackLagCount, lagKey))
	require.NotNil(t, studyServiceCallbackLagBucket.Get(lagKey+",le=0.5"))
}

func TestProcessingObservabilityTracksCommittedAggregateAttentionAndSkip(t *testing.T) {
	outcome := entity.InferenceIngestionProcessingRunOutcomeSuccessWithSkips
	skipCode := entity.InferenceIngestionProcessingJobSkipReasonNoUsableDICOM
	transitionKey := "phase=terminal,outcome=success_with_skips,attention=true,execution_status=skipped,transition=applied"
	transitionBefore := expvarMapIntValue(processingRunTransitionsTotal, transitionKey)
	attentionBefore := expvarMapIntValue(processingRunAttentionTotal, "state_conflict")
	skipBefore := expvarMapIntValue(processingRunSkipsTotal, "no_usable_dicom")

	ObserveProcessingRunTransition(
		entity.InferenceIngestionProcessingRun{
			Phase: entity.InferenceIngestionProcessingRunPhaseTerminal, Outcome: &outcome,
			AttentionRequired: true,
			AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{{
				Code: entity.InferenceIngestionProcessingRunAttentionStateConflict,
			}},
		},
		entity.InferenceIngestionProcessingJob{
			Status: entity.InferenceIngestionProcessingJobStatusSkipped, SkipReasonCode: &skipCode,
		},
		"applied",
	)

	require.Equal(t, transitionBefore+1, expvarMapIntValue(processingRunTransitionsTotal, transitionKey))
	require.Equal(t, attentionBefore+1, expvarMapIntValue(processingRunAttentionTotal, "state_conflict"))
	require.Equal(t, skipBefore+1, expvarMapIntValue(processingRunSkipsTotal, "no_usable_dicom"))
}

func expvarMapIntValue(metric *expvar.Map, key string) int64 {
	value := metric.Get(key)
	if value == nil {
		return 0
	}
	counter, ok := value.(*expvar.Int)
	if !ok {
		return 0
	}
	return counter.Value()
}
