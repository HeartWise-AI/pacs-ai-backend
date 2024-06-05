package rest

import (
	"context"
	"encoding/json"
	"net/http"

	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"

	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	"api-pacs/module/elasticsearch/application"
	"api-pacs/module/elasticsearch/domain/entity"
	types "api-pacs/module/elasticsearch/interfaces/http"
)

// ElasticsearchQueryController request controller for record query
type ElasticsearchQueryController struct {
	application.ElasticsearchQueryServiceInterface
}

// SearchDocumentLogs search document logs from elasticsearch
func (controller *ElasticsearchQueryController) SearchDocumentLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")

	var queryMap map[string]searchTypes.MatchQuery

	err := json.Unmarshal([]byte(query), &queryMap)
	if err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid query.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

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

	var login entity.Login
	var adminMember entity.AdminMember

	switch index {
	case login.GetModelName():
		res, err := controller.ElasticsearchQueryServiceInterface.SearchLoginLogs(context.TODO(), queryMap)
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

		response := viewmodels.HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "Successfully fetched search results for login logs.",
			Data:    logins,
		}

		response.JSON(w)
		return
	case adminMember.GetModelName():
		res, err := controller.ElasticsearchQueryServiceInterface.SearchAdminMemberLogs(context.TODO(), queryMap)
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

		response := viewmodels.HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "Successfully fetched search results for admin member logs.",
			Data:    adminMembers,
		}

		response.JSON(w)
		return
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
}
