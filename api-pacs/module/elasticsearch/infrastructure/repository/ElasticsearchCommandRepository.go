package repository

import (
	"context"
	"errors"
	"time"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	"api-pacs/infrastructures/database/elasticsearch/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/elasticsearch/domain/entity"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

// ElasticsearchCommandRepository handles elasticsearch command repository
type ElasticsearchCommandRepository struct {
	types.ElasticsearchDBHandlerInterface
}

// InsertAdminMemberLog insert admin member log
func (repository *ElasticsearchCommandRepository) InsertAdminMemberLog(ctx context.Context, data repositoryTypes.CreateAdminMemberLog) (*index.Response, error) {
	adminMember := entity.AdminMember{
		TenantID:   data.TenantID,
		TenantName: data.TenantName,
		UserID:     data.UserID,
		Email:      data.Email,
		Name:       data.Name,
		Role:       data.Role,
		LicenseNo:  data.LicenseNo,
		Specialty:  data.Specialty,
		Action:     data.Action,
		Timestamp:  uint(time.Now().Unix()),
	}

	res, err := repository.ElasticsearchDBHandlerInterface.IndexDocument(ctx, adminMember.GetModelName(), adminMember)
	if err != nil {
		return nil, errors.New(apiError.DatabaseError)
	}

	return res, nil
}

// InsertGetModalityStudyLog insert get modality study log
func (repository *ElasticsearchCommandRepository) InsertGetModalityStudyLog(ctx context.Context, data repositoryTypes.CreateGetModalityStudyLog) (*index.Response, error) {
	modalityStudy := entity.ModalityStudy{
		TenantID:   data.TenantID,
		TenantName: data.TenantName,
		ModalityID: data.ModalityID,
		UserID:     data.UserID,
		Email:      data.Email,
		Name:       data.Name,
		QueryID:    data.QueryID,
		Timestamp:  uint(time.Now().Unix()),
	}

	res, err := repository.ElasticsearchDBHandlerInterface.IndexDocument(ctx, modalityStudy.GetModelName(), modalityStudy)
	if err != nil {
		return nil, errors.New(apiError.DatabaseError)
	}

	return res, nil
}

// InsertLoginLog insert login log
func (repository *ElasticsearchCommandRepository) InsertLoginLog(ctx context.Context, data repositoryTypes.CreateLoginLog) (*index.Response, error) {
	login := entity.Login{
		SessionID:  data.SessionID,
		TenantID:   data.TenantID,
		TenantName: data.TenantName,
		UserID:     data.UserID,
		Email:      data.Email,
		Name:       data.Name,
		Role:       data.Role,
		Specialty:  data.Specialty,
		Timestamp:  uint(time.Now().Unix()),
	}

	res, err := repository.ElasticsearchDBHandlerInterface.IndexDocument(ctx, login.GetModelName(), login)
	if err != nil {
		return nil, errors.New(apiError.DatabaseError)
	}

	return res, nil
}

// InsertRetrieveStudyLog insert retrieved study log
func (repository *ElasticsearchCommandRepository) InsertRetrieveStudyLog(ctx context.Context, data repositoryTypes.CreateRetrieveStudyLog) (*index.Response, error) {
	study := entity.RetrievedStudy{
		TenantID:         data.TenantID,
		TenantName:       data.TenantName,
		ModalityID:       data.ModalityID,
		UserID:           data.UserID,
		Email:            data.Email,
		Name:             data.Name,
		StudyInstanceUID: data.StudyInstanceUID,
		Timestamp:        uint(time.Now().Unix()),
	}

	res, err := repository.ElasticsearchDBHandlerInterface.IndexDocument(ctx, study.GetModelName(), study)
	if err != nil {
		return nil, errors.New(apiError.DatabaseError)
	}

	return res, nil
}
