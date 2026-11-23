package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	iamEntity "api-pacs/module/iam/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

func reportCountsRequest(role string, from, to time.Time) types.ListFeedbackReportCounts {
	return types.ListFeedbackReportCounts{TenantID: "tenant-a", Role: role, From: &from, To: &to}
}

func TestFeedbackReportCountsNeedOwnerOrAdmin(t *testing.T) {
	from := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	to := from.Add(30 * 24 * time.Hour)
	for _, role := range []string{iamEntity.UserRole, "", "SUPERUSER"} {
		fake := &predictionsRepositoryFake{}
		service := &InferencePredictionsService{Repository: fake}
		_, err := service.ListFeedbackReportCounts(context.Background(), reportCountsRequest(role, from, to))
		require.EqualError(t, err, apiError.ForbiddenAccess, role)
		require.Empty(t, fake.reportCountQueries)
	}

	fake := &predictionsRepositoryFake{}
	service := &InferencePredictionsService{Repository: fake}
	request := reportCountsRequest(iamEntity.AdminRole, from, to)
	request.TenantID = " "
	_, err := service.ListFeedbackReportCounts(context.Background(), request)
	require.EqualError(t, err, apiError.UnauthorizedAccess)
	require.Empty(t, fake.reportCountQueries)
}

func TestFeedbackReportCountsValidateTheRange(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for name, request := range map[string]types.ListFeedbackReportCounts{
		"missing from":   {TenantID: "tenant-a", Role: iamEntity.OwnerRole, To: &from},
		"missing to":     {TenantID: "tenant-a", Role: iamEntity.OwnerRole, From: &from},
		"empty range":    reportCountsRequest(iamEntity.OwnerRole, from, from),
		"reversed range": reportCountsRequest(iamEntity.OwnerRole, from.Add(time.Hour), from),
		"over 800 days":  reportCountsRequest(iamEntity.OwnerRole, from, from.Add(800*24*time.Hour+time.Second)),
	} {
		fake := &predictionsRepositoryFake{}
		service := &InferencePredictionsService{Repository: fake}
		_, err := service.ListFeedbackReportCounts(context.Background(), request)
		require.EqualError(t, err, apiError.InvalidPayload, name)
		require.Empty(t, fake.reportCountQueries, name)
	}

	fake := &predictionsRepositoryFake{}
	service := &InferencePredictionsService{Repository: fake}
	_, err := service.ListFeedbackReportCounts(context.Background(), reportCountsRequest(iamEntity.OwnerRole, from, from.Add(800*24*time.Hour)))
	require.NoError(t, err, "exactly 800 days is accepted")
}

func TestFeedbackReportCountsUseTheSessionTenantAndMapRows(t *testing.T) {
	location := time.FixedZone("EST", -5*60*60)
	from := time.Date(2026, 10, 24, 0, 0, 0, 0, location)
	to := time.Date(2026, 11, 23, 0, 0, 0, 0, location)
	fake := &predictionsRepositoryFake{reportCounts: []repositoryTypes.ReturnedReportCountRow{
		{ModelName: "PanEcho", ModelVersion: "1.0.3", Modality: "echocardiogram", CompletedCount: 789},
		{ModelName: "DeepCORO-JEPA", ModelVersion: "", Modality: "", CompletedCount: 2},
	}}
	service := &InferencePredictionsService{Repository: fake}

	counts, err := service.ListFeedbackReportCounts(context.Background(), types.ListFeedbackReportCounts{
		TenantID: " tenant-a ", Role: iamEntity.AdminRole, From: &from, To: &to,
	})

	require.NoError(t, err)
	require.Equal(t, []repositoryTypes.CountReturnedReports{{TenantID: "tenant-a", From: from.UTC(), To: to.UTC()}}, fake.reportCountQueries)
	require.Equal(t, from.UTC(), counts.From)
	require.Equal(t, to.UTC(), counts.To)
	require.Equal(t, []types.FeedbackReportCountModel{
		{ModelName: "PanEcho", ModelVersion: "1.0.3", Modality: "echocardiogram", CompletedCount: 789},
		{ModelName: "DeepCORO-JEPA", CompletedCount: 2},
	}, counts.Models)
}

func TestFeedbackReportCountsReturnAnEmptyListAndRepositoryErrors(t *testing.T) {
	from := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	service := &InferencePredictionsService{Repository: &predictionsRepositoryFake{}}
	counts, err := service.ListFeedbackReportCounts(context.Background(), reportCountsRequest(iamEntity.OwnerRole, from, to))
	require.NoError(t, err)
	require.NotNil(t, counts.Models)
	require.Empty(t, counts.Models)

	service = &InferencePredictionsService{Repository: &predictionsRepositoryFake{err: errors.New(apiError.DatabaseError)}}
	_, err = service.ListFeedbackReportCounts(context.Background(), reportCountsRequest(iamEntity.OwnerRole, from, to))
	require.EqualError(t, err, apiError.DatabaseError)
}
