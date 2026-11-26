/*
|--------------------------------------------------------------------------
| Router
|--------------------------------------------------------------------------
|
| This file contains the routes mapping and groupings of your REST API calls.
| See README.md for the routes UI server.
|
*/
package rest

import (
	"expvar"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"api-pacs/interfaces"
	"api-pacs/interfaces/http/rest/middlewares/cors"
	"api-pacs/interfaces/http/rest/middlewares/peeraddr"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	accessEntity "api-pacs/module/access/domain/entity"
)

// ChiRouterInterface declares methods for the chi router
type ChiRouterInterface interface {
	InitRouter() *chi.Mux
	Serve(port int)
}

type router struct{}

type inferenceWorklistHandlers struct {
	getStatuses        http.HandlerFunc
	streamEvents       http.HandlerFunc
	getRunHistory      http.HandlerFunc
	getRunDetail       http.HandlerFunc
	getExecutionResult http.HandlerFunc
	reprocessStudy     http.HandlerFunc
	reprocessStudies   http.HandlerFunc
	// Viewer previous results: reads and single-model runs are available to
	// every authenticated tenant user who accepted the policy, like predict.
	getLatestResults   http.HandlerFunc
	getExecutionReport http.HandlerFunc
	getStudyJobReport  http.HandlerFunc
	runStudyModel      http.HandlerFunc
	getStudyMedia      http.HandlerFunc
	getStudyMediaFile  http.HandlerFunc
	// Cancelling the waiting models of a run is open to the same users as
	// the run detail and the viewer run.
	cancelRun http.HandlerFunc
}

type inferenceFeedbackHandlers struct {
	get    http.HandlerFunc
	update http.HandlerFunc
	remove http.HandlerFunc
}

// mountInferenceFeedbackRoutes keeps user-owned feedback available to every
// authenticated user. Ownership is enforced by the feedback services using the
// tenant and user stored in the authenticated request context.
func mountInferenceFeedbackRoutes(r chi.Router, vote func(http.Handler) http.Handler, handlers inferenceFeedbackHandlers) {
	r.Get("/{modelID}/feedback", handlers.get)
	r.With(vote).Put("/feedback/update", handlers.update)
	r.With(vote).Delete("/{modelID}/feedback/remove", handlers.remove)
}

type inferenceFeedbackAdminHandlers struct {
	list         http.HandlerFunc
	reportCounts http.HandlerFunc
}

// mountInferenceFeedbackAdminRoutes exposes the tenant-wide feedback list and
// the reports-returned counts to users holding feedback.view (every OWNER and
// ADMIN). The services check the permission again.
func mountInferenceFeedbackAdminRoutes(r chi.Router, canView func(http.Handler) http.Handler, handlers inferenceFeedbackAdminHandlers) {
	r.With(canView).Get("/feedback/list", handlers.list)
	r.With(canView).Get("/feedback/report-counts", handlers.reportCounts)
}

type chatFeedbackHandlers struct {
	submit http.HandlerFunc
	remove http.HandlerFunc
}

// mountChatFeedbackRoutes lets a clinician rate a chat answer (PACS-448); the
// chat backend checks the conversation is theirs.
func mountChatFeedbackRoutes(r chi.Router, vote func(http.Handler) http.Handler, handlers chatFeedbackHandlers) {
	r.With(vote).Put("/threads/{threadID}/messages/{messageID}/feedback", handlers.submit)
	r.With(vote).Delete("/threads/{threadID}/messages/{messageID}/feedback", handlers.remove)
}

type chatFeedbackAdminHandlers struct {
	list   http.HandlerFunc
	counts http.HandlerFunc
}

// mountChatFeedbackAdminRoutes exposes the workspace's chat answer votes to
// users holding feedback.view, apart from the model-prediction feedback.
func mountChatFeedbackAdminRoutes(r chi.Router, canView func(http.Handler) http.Handler, handlers chatFeedbackAdminHandlers) {
	r.With(canView).Get("/feedback/list", handlers.list)
	r.With(canView).Get("/feedback/counts", handlers.counts)
}

type iamHandoffHandlers struct {
	exchange http.HandlerFunc
	link     http.HandlerFunc
}

// mountIAMHandoffRoutes exposes the sign-in handoff (CA-268): exchanging a
// one-time code is public, linking an account needs a PACS-AI session.
func mountIAMHandoffRoutes(r chi.Router, authenticate func(http.Handler) http.Handler, handlers iamHandoffHandlers) {
	r.Post("/handoff/exchange", handlers.exchange)
	r.With(authenticate).Post("/handoff/link", handlers.link)
}

type userViewerTourHandlers struct {
	get      http.HandlerFunc
	complete http.HandlerFunc
	reset    http.HandlerFunc
}

// mountUserViewerTourRoutes exposes the viewer onboarding tour state. The
// routes carry no user id: handlers act on the user stored in the
// authenticated request context, so callers must mount them behind
// TokenSessionAuthGuard.
func mountUserViewerTourRoutes(r chi.Router, handlers userViewerTourHandlers) {
	r.Get("/viewer-tour", handlers.get)
	r.Put("/viewer-tour/complete", handlers.complete)
	r.Post("/viewer-tour/reset", handlers.reset)
}

// mountInferenceWorklistRoutes keeps authentication and policy acceptance
// structurally attached to every tenant-scoped route; running a model needs
// models.use and reprocessing needs processing.manage plus access to every
// model, since a reprocess dispatches all of the study's ingestion models.
func mountInferenceWorklistRoutes(
	r chi.Router,
	authenticate func(http.Handler) http.Handler,
	acceptPolicy func(http.Handler) http.Handler,
	useModels func(http.Handler) http.Handler,
	manageProcessing func(http.Handler) http.Handler,
	allModels func(http.Handler) http.Handler,
	handlers inferenceWorklistHandlers,
) {
	r.Group(func(r chi.Router) {
		r.Use(authenticate)
		r.Use(acceptPolicy)
		r.Get("/worklist/status", handlers.getStatuses)
		r.Get("/worklist/events", handlers.streamEvents)
		r.Get("/worklist/studies/{studyInstanceUID}/runs", handlers.getRunHistory)
		r.Get("/processing/runs/{runId}", handlers.getRunDetail)
		r.Get("/processing/runs/{runId}/executions/{executionId}/result", handlers.getExecutionResult)
		r.Get("/worklist/studies/{studyInstanceUID}/latest-results", handlers.getLatestResults)
		r.Get("/processing/runs/{runId}/executions/{executionId}/report", handlers.getExecutionReport)
		r.Get("/worklist/studies/{studyInstanceUID}/jobs/{studyServiceJobId}/report", handlers.getStudyJobReport)
		r.With(useModels).Post("/worklist/studies/{studyInstanceUID}/models/{modelId}/run", handlers.runStudyModel)
		r.Get("/worklist/studies/{studyInstanceUID}/media", handlers.getStudyMedia)
		r.Get("/media/{mediaId}", handlers.getStudyMediaFile)
		r.Post("/processing/runs/{runId}/cancel", handlers.cancelRun)

		r.Group(func(r chi.Router) {
			r.Use(manageProcessing)
			r.Use(allModels)
			r.Post("/worklist/studies/reprocess-batch", handlers.reprocessStudies)
			r.Post("/worklist/studies/{studyInstanceUID}/reprocess", handlers.reprocessStudy)
		})
	})
}

type inferencePredictionsHandlers struct {
	list    http.HandlerFunc
	history http.HandlerFunc
	export  http.HandlerFunc
}

// mountInferencePredictionsRoutes exposes the AI Predictions page to every
// authenticated tenant user who accepted the policies. The "all" view is
// limited to workspace administrators by the service, which reads the role
// from the authenticated request context; "mine" always means that user.
func mountInferencePredictionsRoutes(
	r chi.Router,
	authenticate func(http.Handler) http.Handler,
	acceptPolicy func(http.Handler) http.Handler,
	handlers inferencePredictionsHandlers,
) {
	r.Group(func(r chi.Router) {
		r.Use(authenticate)
		r.Use(acceptPolicy)
		r.Get("/predictions", handlers.list)
		r.Get("/predictions/export.csv", handlers.export)
		r.Get("/predictions/{studyInstanceUID}/history", handlers.history)
	})
}

type userMemberGuards struct {
	invite       func(http.Handler) http.Handler
	manage       func(http.Handler) http.Handler
	ownerOrAdmin func(http.Handler) http.Handler
}

type userMemberHandlers struct {
	add, invite, resendInvite, listInvites, removeInvite http.HandlerFunc
	listUsers, policyStatus, update, suspend, reactivate http.HandlerFunc
	remove                                               http.HandlerFunc
	myAccess, listRoles, createRole, updateRole          http.HandlerFunc
	deleteRole, assignRole                               http.HandlerFunc
}

// mountUserMemberRoutes exposes member and role management. Invitations need
// members.invite, member administration needs members.manage, and custom
// roles can be created, changed or assigned only by OWNER and ADMIN. Mount
// behind TokenSessionAuthGuard.
func mountUserMemberRoutes(r chi.Router, guards userMemberGuards, handlers userMemberHandlers) {
	r.Get("/me/access", handlers.myAccess)

	r.Group(func(r chi.Router) {
		r.Use(guards.invite)
		r.Post("/add", handlers.add)
		r.Post("/invite", handlers.invite)
		r.Post("/invite/resend", handlers.resendInvite)
		r.Get("/invites", handlers.listInvites)
		r.Delete("/invite/{ID}/remove", handlers.removeInvite)
	})

	r.Group(func(r chi.Router) {
		r.Use(guards.manage)
		r.Get("/all", handlers.listUsers)
		r.Get("/roles", handlers.listRoles)
		r.Get("/{ID}/policies/status", handlers.policyStatus)
		r.Put("/update", handlers.update)
		r.Post("/{ID}/suspend", handlers.suspend)
		r.Post("/{ID}/reactivate", handlers.reactivate)
		r.Delete("/{ID}/remove", handlers.remove)
	})

	r.Group(func(r chi.Router) {
		r.Use(guards.ownerOrAdmin)
		r.Post("/roles", handlers.createRole)
		r.Put("/roles/{roleID}", handlers.updateRole)
		r.Delete("/roles/{roleID}", handlers.deleteRole)
		r.Put("/{ID}/custom-role", handlers.assignRole)
	})
}

var (
	m          *router
	routerOnce sync.Once
)

// InitRouter initializes main routes
func (router *router) InitRouter() *chi.Mux {
	// DI assignment
	dockerInferenceProxy := interfaces.ServiceContainer().RegisterDockerInferenceProxy()
	elasticsearchCommandController := interfaces.ServiceContainer().RegisterElasticsearchRESTCommandController()
	elasticsearchQueryController := interfaces.ServiceContainer().RegisterElasticsearchRESTQueryController()
	iamMiddleware := interfaces.ServiceContainer().RegisterIAMRESTMiddleware()
	iamCommandController := interfaces.ServiceContainer().RegisterIAMRESTCommandController()
	iamHandoffController := interfaces.ServiceContainer().RegisterIAMHandoffController()
	inferenceCommandController := interfaces.ServiceContainer().RegisterInferenceRESTCommandController()
	inferenceQueryController := interfaces.ServiceContainer().RegisterInferenceRESTQueryController()
	inferencePredictionsController := interfaces.ServiceContainer().RegisterInferenceRESTPredictionsController()
	leadCommandController := interfaces.ServiceContainer().RegisterLeadRESTCommandController()
	orchestratorController := interfaces.ServiceContainer().RegisterOrchestratorRESTController()
	orthancProxy := interfaces.ServiceContainer().RegisterOrthancProxy()
	orthancCommandController := interfaces.ServiceContainer().RegisterOrthancRESTCommandController()
	orthancQueryController := interfaces.ServiceContainer().RegisterOrthancRESTQueryController()
	tenantCommandController := interfaces.ServiceContainer().RegisterTenantRESTCommandController()
	tenantQueryController := interfaces.ServiceContainer().RegisterTenantRESTQueryController()
	userCommandController := interfaces.ServiceContainer().RegisterUserRESTCommandController()
	userQueryController := interfaces.ServiceContainer().RegisterUserRESTQueryController()
	accessController := interfaces.ServiceContainer().RegisterAccessRESTController()

	// create router
	r := chi.NewRouter()

	// global and recommended middlewares
	r.Use(middleware.RequestID)
	// Capture the real socket peer before RealIP rewrites RemoteAddr from headers.
	r.Use(peeraddr.Capture)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(cors.Init().Handler)
	r.Use(middleware.Recoverer)

	// default route
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		response := viewmodels.HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "alive",
			Data: map[string]interface{}{
				"version": "v0.27.0-beta",
			},
		}

		response.JSON(w)
	})

	// docs routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.BasicAuth(os.Getenv("API_NAME"), map[string]string{
			"sudo": os.Getenv("OPENAPI_DOCS_PASSWORD"),
		}))

		workDir, _ := os.Getwd()
		docsDir := http.Dir(filepath.Join(workDir, "docs"))
		FileServer(r, "/docs", docsDir)
		r.Handle("/debug/vars", expvar.Handler())

		// orthanc openapi
		orthancDocsDir := http.Dir(filepath.Join(workDir, "docs", "orthanc"))
		FileServer(r, "/docs/orthanc", orthancDocsDir)
	})

	// API routes
	r.Group(func(r chi.Router) {
		r.Route("/internal/v1/inference", func(r chi.Router) {
			r.Use(requestbody.WithReadDeadline(time.Duration(requestbody.PositiveInt64FromEnvironment(
				"INTERNAL_INFERENCE_READ_TIMEOUT_SECONDS",
				int64(requestbody.DefaultDICOMWebReadTimeout/time.Second),
			)) * time.Second))
			r.Use(requestbody.LimitWithScope(requestbody.PositiveInt64FromEnvironment(
				"INTERNAL_INFERENCE_PREDICT_MAX_REQUEST_BODY_BYTES",
				2*1024*1024*1024,
			), "internal_inference"))
			r.Post("/predict", inferenceCommandController.PredictPreparedInferenceModel)
		})

		r.Route("/v1", func(r chi.Router) {
			r.Use(requestbody.Limit(requestbody.PositiveInt64FromEnvironment(
				"API_MAX_REQUEST_BODY_BYTES",
				requestbody.DefaultAPIMaxBytes,
			)))

			// elasticsearch module
			r.Route("/ecs", func(r chi.Router) {
				// superuser only
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.FirebaseSuperUserGuard)

					r.Patch("/kibana/indices/sync", elasticsearchCommandController.SyncKibanaIndices)
				})

				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)
					r.Use(iamMiddleware.RequirePermission(accessEntity.PermLogsView))

					r.Get("/logs", elasticsearchQueryController.SearchDocumentLogs)
				})
			})

			// iam module
			r.Route("/iam", func(r chi.Router) {
				r.Post("/login", iamCommandController.LoginTenantUser)
				r.Post("/forgot-password", iamCommandController.ForgotTenantUserPassword)
				r.Post("/verify-email", iamCommandController.VerifyTenantUserEmail)
				mountIAMHandoffRoutes(r, iamMiddleware.TokenSessionAuthGuard, iamHandoffHandlers{
					exchange: iamHandoffController.ExchangeHandoff,
					link:     iamHandoffController.CompleteHandoffLink,
				})
			})

			// inference module
			r.Route("/inference", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.FirebaseSuperUserGuard)
					r.Post("/model/versions/publish", inferenceCommandController.PublishInferenceModelVersion)
				})
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)
					r.Use(iamMiddleware.PolicyAcceptanceGuard)
					r.Get("/quota", inferenceQueryController.GetInferenceQuota)
					r.With(iamMiddleware.RequirePermission(accessEntity.PermModelsManage)).Get("/gpus", inferenceCommandController.GetGPUs)
					r.With(iamMiddleware.RequirePermission(accessEntity.PermModelsManage)).Get("/gpus/placement-plan", inferenceCommandController.GetGPUPlacementPlan)

					r.Post("/onboarding-model-questionnaire-answers/add", inferenceCommandController.AddOnboardingModelQuestionnaireAnswers)
					r.Get("/onboarding-model-questionnaire-answers", inferenceQueryController.GetOnboardingModelQuestionnaireAnswers)

					// Chat answer feedback for the admin panel (PACS-448)
					r.Route("/chat", func(r chi.Router) {
						mountChatFeedbackAdminRoutes(r, iamMiddleware.RequirePermission(accessEntity.PermFeedbackView), chatFeedbackAdminHandlers{
							list:   orchestratorController.ListChatFeedback,
							counts: orchestratorController.CountChatFeedback,
						})
					})

					r.Route("/model", func(r chi.Router) {
						mountInferenceFeedbackRoutes(r, iamMiddleware.RequirePermission(accessEntity.PermFeedbackVote), inferenceFeedbackHandlers{
							get:    inferenceQueryController.GetModelFeedbackByModelID,
							update: inferenceCommandController.UpdateModelFeedback,
							remove: inferenceCommandController.RemoveModelFeedback,
						})
						mountInferenceFeedbackAdminRoutes(r, iamMiddleware.RequirePermission(accessEntity.PermFeedbackView), inferenceFeedbackAdminHandlers{
							list:         inferenceQueryController.ListModelFeedback,
							reportCounts: inferencePredictionsController.ListFeedbackReportCounts,
						})

						r.Group(func(r chi.Router) {
							r.Use(iamMiddleware.RequirePermission(accessEntity.PermModelsManage))

							// inference model routes
							r.Post("/add", inferenceCommandController.AddInferenceModel)
							r.Get("/list", inferenceQueryController.GetInferenceModels)
							r.Get("/{ID}/versions", inferenceQueryController.GetInferenceModelVersions)
							r.Post("/{ID}/versions/{versionID}/deploy", inferenceCommandController.DeployInferenceModelVersion)
							r.Post("/{ID}/gpu-placement", inferenceCommandController.MoveInferenceModelGPU)
							r.Get("/{ID}/deployments/{deploymentID}", inferenceCommandController.GetInferenceModelDeploymentOperation)
							r.Get("/{ID}/deployments/{deploymentID}/events", inferenceCommandController.StreamInferenceModelDeploymentEvents)
							r.Delete("/{ID}/remove", inferenceCommandController.RemoveInferenceModel)
							r.Put("/{ID}/update", inferenceCommandController.UpdateInferenceModel)

							// container routes
							r.Route("/container", func(r chi.Router) {
								r.Post("/{containerID}/restart", inferenceCommandController.RestartInferenceModelContainer)
								r.Post("/{containerID}/start", inferenceCommandController.StartInferenceModelContainer)
								r.Post("/{containerID}/stop", inferenceCommandController.StopInferenceModelContainer)
								r.Get("/{containerID}/info", inferenceQueryController.GetContainerInfo)
							})
						})

						// proxy routes
						r.Route("/proxy", func(r chi.Router) {
							r.With(iamMiddleware.RequirePermission(accessEntity.PermModelsUse)).Post("/container/{containerID}/predict", inferenceCommandController.PredictInferenceModel)
							r.Get("/container/{containerID}/info", inferenceQueryController.GetInferenceModelInfo)
							r.Get("/container/{containerID}/facts", inferenceQueryController.GetInferenceModelFacts)
							r.Get("/available", inferenceQueryController.GetInferenceAvailableModels)
						})
					})

					// inference ingestion jobs
					r.Route("/ingestion", func(r chi.Router) {
						r.Group(func(r chi.Router) {
							r.Use(iamMiddleware.RequirePermission(accessEntity.PermProcessingManage))
							// Ingestion jobs dispatch every ingestion model.
							r.Use(iamMiddleware.RequireAllModels)

							r.Post("/job/create", inferenceCommandController.CreateInferenceIngestionJob)
							r.Post("/job/{ID}/start", inferenceCommandController.StartInferenceIngestionJob)
							r.Post("/job/{ID}/stop", inferenceCommandController.StopInferenceInferenceJob)
							r.Post("/jobs/import", inferenceCommandController.ImportInferenceIngestionJobsCSVFile)
							r.Get("/jobs", inferenceQueryController.GetInferenceIngestionJobs)
							r.Get("/candidates", inferenceQueryController.GetInferenceIngestionCandidates)
							r.Get("/retrieval-failures", inferenceQueryController.GetInferenceIngestionRetrievalFailures)
							r.Get("/job/{ID}/candidates", inferenceQueryController.GetInferenceIngestionJobCandidates)
							r.Get("/job/{ID}/study/{studyInstanceUID}", inferenceQueryController.GetInferenceIngestionCandidateByStudyUID)
							r.Put("/job/{ID}/update", inferenceCommandController.UpdateInferenceIngestionJob)
							r.Delete("/job/{ID}/remove", inferenceCommandController.RemoveInferenceIngestionJob)
						})
					})

				})

				mountInferenceWorklistRoutes(
					r,
					iamMiddleware.TokenSessionAuthGuard,
					iamMiddleware.PolicyAcceptanceGuard,
					iamMiddleware.RequirePermission(accessEntity.PermModelsUse),
					iamMiddleware.RequirePermission(accessEntity.PermProcessingManage),
					iamMiddleware.RequireAllModels,
					inferenceWorklistHandlers{
						getStatuses:        inferenceQueryController.GetWorklistStudyStatuses,
						streamEvents:       inferenceQueryController.StreamWorklistEvents,
						getRunHistory:      inferenceQueryController.GetStudyProcessingRunHistory,
						getRunDetail:       inferenceQueryController.GetProcessingRunDetail,
						getExecutionResult: inferenceQueryController.GetProcessingRunExecutionResult,
						reprocessStudy:     inferenceCommandController.ReprocessStudy,
						reprocessStudies:   inferenceCommandController.ReprocessStudies,
						getLatestResults:   inferenceQueryController.GetStudyLatestModelResults,
						getExecutionReport: inferenceQueryController.GetProcessingRunExecutionReport,
						getStudyJobReport:  inferenceQueryController.GetStudyJobReport,
						runStudyModel:      inferenceCommandController.RunStudyModel,
						getStudyMedia:      inferenceQueryController.GetStudyMedia,
						getStudyMediaFile:  inferenceQueryController.GetStudyMediaFile,
						cancelRun:          inferenceCommandController.CancelProcessingRun,
					},
				)

				mountInferencePredictionsRoutes(
					r,
					iamMiddleware.TokenSessionAuthGuard,
					iamMiddleware.PolicyAcceptanceGuard,
					inferencePredictionsHandlers{
						list:    inferencePredictionsController.ListPredictions,
						history: inferencePredictionsController.GetPredictionModelHistory,
						export:  inferencePredictionsController.ExportPredictions,
					},
				)
			})

			r.Route("/internal", func(r chi.Router) {
				// Sign-in handoff minted by CardioAgent's backend (service token,
				// internal network only).
				r.Post("/auth/handoff", iamHandoffController.MintHandoff)
				r.Route("/inference", func(r chi.Router) {
					r.Post("/ingestion/candidates/{candidate_id}/processing", inferenceCommandController.StudyServiceProcessingCallback)
				})
			})

			// lead module
			r.Route("/lead", func(r chi.Router) {
				r.Post("/contact-form", leadCommandController.AddContactForm)
				r.Post("/subscribe", leadCommandController.Subscribe)
			})

			// orchestrator module
			r.Route("/orchestrator", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)
					r.Use(iamMiddleware.PolicyAcceptanceGuard)

					// Thread management
					r.Post("/threads", orchestratorController.CreateThread)
					r.Get("/threads/{threadID}", orchestratorController.GetThread)

					// Message and payload management
					r.Post("/threads/{threadID}/chat", orchestratorController.CreateMessage)
					r.Get("/threads/{threadID}/chat/progress", orchestratorController.GetChatProgress)
					r.Post("/threads/{threadID}/dicom", orchestratorController.UploadDicomPayload)

					// Thumbs up/down on chat answers
					mountChatFeedbackRoutes(r, iamMiddleware.RequirePermission(accessEntity.PermFeedbackVote), chatFeedbackHandlers{
						submit: orchestratorController.SubmitChatFeedback,
						remove: orchestratorController.RemoveChatFeedback,
					})
				})
			})

			// orthanc module
			r.Route("/orthanc", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)
					r.Use(iamMiddleware.PolicyAcceptanceGuard)

					r.Post("/modality/studies", orthancQueryController.FindModalityStudies)
					r.Post("/modality/retrieve", orthancCommandController.RetrieveModalityStudy)
					r.Post("/modality/{modalityID}/study/{studyInstanceUID}/series/store", orthancCommandController.StoreStudyCustomSeries)
					r.Get("/jobs", orthancQueryController.GetJobsInfo)
					r.Get("/modalities/list", orthancQueryController.ListDICOMModalities)
					r.Get("/modality/{modalityID}/linked/storage/enabled", orthancQueryController.GetLinkedDICOMModalityWithEnabledCStore)
					r.Get("/sop-instance/{sopInstanceUID}/find", orthancQueryController.FindLocalSOPInstance)

					// admin or owner only
					r.Group(func(r chi.Router) {
						r.Use(iamMiddleware.RequirePermission(accessEntity.PermDicomManage))

						r.Post("/modality/{modalityID}/echo", orthancCommandController.TriggerDICOMEchoSCU)
						r.Put("/modality/{modalityID}/update", orthancCommandController.UpdateDICOMModality)
						r.Delete("/modality/{modalityID}/remove", orthancCommandController.RemoveDICOMModality)
					})
				})
			})

			// tenant module
			r.Route("/tenant", func(r chi.Router) {
				r.Get("/public", tenantQueryController.GetPublicTenantByID)

				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)

					r.Post("/onboarding-questionnaire-answers/add", tenantCommandController.AddOnboardingQuestionnaireAnswers)
					r.Get("/onboarding-questionnaire-answers", tenantQueryController.GetOnboardingQuestionnaireAnswers)
					r.Get("/", tenantQueryController.GetTenantByID)
					r.With(iamMiddleware.RequirePermission(accessEntity.PermWorkspaceManage)).Put("/onboarding-consent/config/update", tenantCommandController.UpdateOnboardingConsentConfig)
					r.With(iamMiddleware.RequirePermission(accessEntity.PermWorkspaceManage)).Put("/onboarding-registration/config/update", tenantCommandController.UpdateOnboardingRegistrationConfig)
				})
			})

			// user module
			r.Route("/user", func(r chi.Router) {
				r.Post("/register", userCommandController.RegisterTenantUser)
				r.Get("/specialties", userQueryController.GetDoctorSpecialties)
				r.Get("/policies/registration", userQueryController.GetRegistrationPolicies)

				// superuser only
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.FirebaseSuperUserGuard)

					r.Post("/owner/add", userCommandController.CreateTenantOwner)
				})

				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)

					r.Get("/policies/status", userQueryController.GetCurrentUserPolicyStatus)
					r.Post("/policies/accept", userCommandController.AcceptPolicies)
					r.Post("/tutorial/reset", userCommandController.ResetTutorial)
					r.Get("/me", userQueryController.GetCurrentTenantUser)
					r.Get("/metadata", userQueryController.GetUserMetadata)
					r.Put("/password/update", userCommandController.UpdateTenantUserPassword)
					r.Put("/metadata/update", userCommandController.UpdateUserMetadata)
					mountUserViewerTourRoutes(r, userViewerTourHandlers{
						get:      userQueryController.GetUserViewerTour,
						complete: userCommandController.CompleteUserViewerTour,
						reset:    userCommandController.ResetUserViewerTour,
					})

					mountUserMemberRoutes(r, userMemberGuards{
						invite:       iamMiddleware.RequirePermission(accessEntity.PermMembersInvite),
						manage:       iamMiddleware.RequirePermission(accessEntity.PermMembersManage),
						ownerOrAdmin: iamMiddleware.RBACOwnerOrAdminGuard,
					}, userMemberHandlers{
						add:          userCommandController.CreateTenantUser,
						invite:       userCommandController.SendTenantEmailInvite,
						resendInvite: userCommandController.ResendTenantEmailInvite,
						listInvites:  userQueryController.GetTenantUserEmailInvites,
						removeInvite: userCommandController.RemoveTenantUserEmailInvite,
						listUsers:    userQueryController.GetTenantUsers,
						policyStatus: userQueryController.GetTenantUserPolicyStatus,
						update:       userCommandController.UpdateTenantUser,
						suspend:      userCommandController.SuspendTenantUser,
						reactivate:   userCommandController.ReactivateTenantUser,
						remove:       userCommandController.DeleteTenantUser,
						myAccess:     accessController.GetMyAccess,
						listRoles:    accessController.ListRoles,
						createRole:   accessController.CreateRole,
						updateRole:   accessController.UpdateRole,
						deleteRole:   accessController.DeleteRole,
						assignRole:   accessController.AssignCustomRole,
					})
				})
			})
		})

		// proxy routes
		r.Route("/proxy", func(r chi.Router) {
			r.Handle("/docker-inference/{containerName}/app/viewer/*", dockerInferenceProxy.AppViewerProxy())

			// protected routes
			r.Group(func(r chi.Router) {
				r.Use(iamMiddleware.TokenSessionOrthancProxyAuthGuard)
				r.Use(iamMiddleware.PolicyAcceptanceGuard)

				dicomWebProxy := requestbody.LimitWithScope(requestbody.PositiveInt64FromEnvironment(
					"DICOMWEB_MAX_REQUEST_BODY_BYTES",
					requestbody.DefaultDICOMWebMaxBytes,
				), "dicomweb")(orthancProxy.DICOMWebProxy())
				dicomWebReadTimeout := time.Duration(requestbody.PositiveInt64FromEnvironment(
					"DICOMWEB_READ_TIMEOUT_SECONDS",
					int64(requestbody.DefaultDICOMWebReadTimeout/time.Second),
				)) * time.Second
				r.Handle("/orthanc/dicom-web/*", requestbody.WithReadDeadline(dicomWebReadTimeout)(dicomWebProxy))
			})
		})
	})

	return r
}

// FileServer conveniently sets up a http.FileServer handler to serve
// static files from a http.FileSystem.
func FileServer(r chi.Router, path string, root http.FileSystem) {
	if strings.ContainsAny(path, "{}*") {
		panic("FileServer does not permit any URL parameters.")
	}

	if path != "/" && path[len(path)-1] != '/' {
		r.Get(path, http.RedirectHandler(path+"/", 301).ServeHTTP)
		path += "/"
	}
	path += "*"

	r.Get(path, func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		pathPrefix := strings.TrimSuffix(rctx.RoutePattern(), "/*")
		fs := http.StripPrefix(pathPrefix, http.FileServer(root))
		fs.ServeHTTP(w, r)
	})
}

func (router *router) Serve(port int) {
	log.Printf("[SERVER] REST server running on :%d", port)
	server := newHTTPServer(port, router.InitRouter())
	err := server.ListenAndServe()
	if err != nil {
		log.Fatalf("[SERVER] REST server failed %v", err)
	}
}

const (
	defaultReadHeaderTimeout = 10 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultIdleTimeout       = 2 * time.Minute
)

func newHTTPServer(port int, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: configuredServerDuration("HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS", defaultReadHeaderTimeout),
		ReadTimeout:       configuredServerDuration("HTTP_SERVER_READ_TIMEOUT_SECONDS", defaultReadTimeout),
		IdleTimeout:       configuredServerDuration("HTTP_SERVER_IDLE_TIMEOUT_SECONDS", defaultIdleTimeout),
		// Streaming worklist events intentionally prevent a server-wide
		// WriteTimeout. Nginx and non-streaming upstream calls remain bounded.
		WriteTimeout: 0,
	}
}

func configuredServerDuration(name string, fallback time.Duration) time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func registerHandlers() {}

// ChiRouter export instantiated chi router once
func ChiRouter() ChiRouterInterface {
	if m == nil {
		routerOnce.Do(func() {
			// register http handlers
			registerHandlers()

			m = &router{}
		})
	}
	return m
}
