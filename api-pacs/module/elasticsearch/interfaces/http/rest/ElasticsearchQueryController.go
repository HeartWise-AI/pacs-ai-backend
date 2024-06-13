package rest

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gocarina/gocsv"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	"api-pacs/module/elasticsearch/application"
	"api-pacs/module/elasticsearch/domain/entity"
	serviceTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	types "api-pacs/module/elasticsearch/interfaces/http"
)

// ElasticsearchQueryController request controller for record query
type ElasticsearchQueryController struct {
	application.ElasticsearchQueryServiceInterface
}

// SearchDocumentLogs search document logs from elasticsearch
func (controller *ElasticsearchQueryController) SearchDocumentLogs(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	// required fields
	index := r.URL.Query().Get("index")
	if len(index) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid index.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	query := r.URL.Query().Get("query")
	if len(index) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid query.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	startDateStr := r.URL.Query().Get("startDate")
	startDate, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid start date.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	endDateStr := r.URL.Query().Get("endDate")
	endDate, err := time.Parse("2006-01-02", endDateStr)
	if err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid end date.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// option to export response to csv
	export := r.URL.Query().Get("export")
	isExport, _ := strconv.ParseBool(export)

	var login entity.Login
	var adminMember entity.AdminMember
	var modalityStudy entity.ModalityStudy
	var retrievedStudy entity.RetrievedStudy

	searchDocument := serviceTypes.SearchDocument{
		TenantID:  tenantID,
		Query:     query,
		StartDate: uint(startDate.Unix()),
		EndDate:   uint(endDate.Unix()),
	}

	// TODO: refactor with Go generics

	var logs interface{}
	var message string
	var indexName string

	switch index {
	case login.GetModelName():
		res, err := controller.ElasticsearchQueryServiceInterface.SearchLoginLogs(context.TODO(), searchDocument)
		if err != nil && err.Error() != errors.MissingRecord {
			var httpCode int
			var errorMsg string

			switch err.Error() {
			default:
				httpCode = http.StatusInternalServerError
				errorMsg = "Database error."
			}

			response := viewmodels.HTTPResponseVM{
				Status:    httpCode,
				Success:   false,
				Message:   errorMsg,
				ErrorCode: err.Error(),
			}

			response.JSON(w)
			return
		}

		logins := []types.LoginLogResponse{}

		for _, login := range res {
			logins = append(logins, types.LoginLogResponse{
				SessionID:  login.SessionID,
				TenantID:   login.TenantID,
				TenantName: login.TenantName,
				UserID:     login.UserID,
				Email:      login.Email,
				Name:       login.Name,
				Role:       login.Role,
				Specialty:  login.Specialty,
				Timestamp:  login.Timestamp,
			})
		}

		logs = logins
		message = "Successfully fetched search results for login logs."
		indexName = login.GetModelName()
	case adminMember.GetModelName():
		res, err := controller.ElasticsearchQueryServiceInterface.SearchAdminMemberLogs(context.TODO(), searchDocument)
		if err != nil && err.Error() != errors.MissingRecord {
			var httpCode int
			var errorMsg string

			switch err.Error() {
			default:
				httpCode = http.StatusInternalServerError
				errorMsg = "Database error."
			}

			response := viewmodels.HTTPResponseVM{
				Status:    httpCode,
				Success:   false,
				Message:   errorMsg,
				ErrorCode: err.Error(),
			}

			response.JSON(w)
			return
		}

		adminMembers := []types.AdminMemberLogResponse{}

		for _, adminMember := range res {
			adminMembers = append(adminMembers, types.AdminMemberLogResponse{
				TenantID:   adminMember.TenantID,
				TenantName: adminMember.TenantName,
				UserID:     adminMember.UserID,
				Email:      adminMember.Email,
				Name:       adminMember.Name,
				Role:       adminMember.Role,
				LicenseNo:  adminMember.LicenseNo,
				Specialty:  adminMember.Specialty,
				Action:     adminMember.Action,
				Timestamp:  adminMember.Timestamp,
			})

		}
		log.Print("this is from admin member controller")

		logs = adminMembers
		message = "Successfully fetched search results for admin member logs."
		indexName = adminMember.GetModelName()
	case modalityStudy.GetModelName():
		res, err := controller.ElasticsearchQueryServiceInterface.SearchModalityStudyLogs(context.TODO(), searchDocument)
		if err != nil && err.Error() != errors.MissingRecord {
			var httpCode int
			var errorMsg string

			switch err.Error() {
			default:
				httpCode = http.StatusInternalServerError
				errorMsg = "Database error."
			}

			response := viewmodels.HTTPResponseVM{
				Status:    httpCode,
				Success:   false,
				Message:   errorMsg,
				ErrorCode: err.Error(),
			}

			response.JSON(w)
			return
		}

		modalityStudies := []types.ModalityStudyLogResponse{}

		for _, modalityStudy := range res {
			modalityStudies = append(modalityStudies, types.ModalityStudyLogResponse{
				TenantID:   modalityStudy.TenantID,
				TenantName: modalityStudy.TenantName,
				TenantAET:  modalityStudy.TenantAET,
				UserID:     modalityStudy.UserID,
				Email:      modalityStudy.Email,
				Name:       modalityStudy.Name,
				QueryID:    modalityStudy.QueryID,
				Timestamp:  modalityStudy.Timestamp,
			})
		}

		logs = modalityStudies
		message = "Successfully fetched search results for modality study logs."
		indexName = modalityStudy.GetModelName()
	case retrievedStudy.GetModelName():
		res, err := controller.ElasticsearchQueryServiceInterface.SearchRetrievedStudyLogs(context.TODO(), searchDocument)
		if err != nil && err.Error() != errors.MissingRecord {
			var httpCode int
			var errorMsg string

			switch err.Error() {
			default:
				httpCode = http.StatusInternalServerError
				errorMsg = "Database error."
			}

			response := viewmodels.HTTPResponseVM{
				Status:    httpCode,
				Success:   false,
				Message:   errorMsg,
				ErrorCode: err.Error(),
			}

			response.JSON(w)
			return
		}

		retrievedStudies := []types.RetrievedStudyLogResponse{}

		for _, retrievedStudy := range res {
			retrievedStudies = append(retrievedStudies, types.RetrievedStudyLogResponse{
				TenantID:         retrievedStudy.TenantID,
				TenantName:       retrievedStudy.TenantName,
				TenantAET:        retrievedStudy.TenantAET,
				UserID:           retrievedStudy.UserID,
				Email:            retrievedStudy.Email,
				Name:             retrievedStudy.Name,
				StudyInstanceUID: retrievedStudy.StudyInstanceUID,
				QueryID:          retrievedStudy.QueryID,
				AnswerIndex:      retrievedStudy.AnswerIndex,
				Timestamp:        retrievedStudy.Timestamp,
			})
		}

		logs = retrievedStudies
		message = "Successfully fetched search results for retrieved study logs."
		indexName = retrievedStudy.GetModelName()
	default:
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusNotFound,
			Success:   false,
			Message:   "No records found.",
			ErrorCode: errors.MissingRecord,
		}

		response.JSON(w)
		return
	}

	if !isExport {
		response := viewmodels.HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: message,
			Data:    logs,
		}

		response.JSON(w)
		return
	}

	// return csv file
	filename := fmt.Sprintf("%s_export_%s.csv", time.Now().Format("2006-01-02"), indexName)

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment;filename=%s", filename))
	gocsv.Marshal(logs, w)
	w.WriteHeader(http.StatusOK)
}
