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
)

// ChiRouterInterface declares methods for the chi router
type ChiRouterInterface interface {
	InitRouter() *chi.Mux
	Serve(port int)
}

type router struct{}

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
	inferenceCommandController := interfaces.ServiceContainer().RegisterInferenceRESTCommandController()
	inferenceQueryController := interfaces.ServiceContainer().RegisterInferenceRESTQueryController()
	leadCommandController := interfaces.ServiceContainer().RegisterLeadRESTCommandController()
	orchestratorController := interfaces.ServiceContainer().RegisterOrchestratorRESTController()
	orthancProxy := interfaces.ServiceContainer().RegisterOrthancProxy()
	orthancCommandController := interfaces.ServiceContainer().RegisterOrthancRESTCommandController()
	orthancQueryController := interfaces.ServiceContainer().RegisterOrthancRESTQueryController()
	tenantCommandController := interfaces.ServiceContainer().RegisterTenantRESTCommandController()
	tenantQueryController := interfaces.ServiceContainer().RegisterTenantRESTQueryController()
	userCommandController := interfaces.ServiceContainer().RegisterUserRESTCommandController()
	userQueryController := interfaces.ServiceContainer().RegisterUserRESTQueryController()

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
					r.Use(iamMiddleware.RBACOwnerOrAdminGuard)

					r.Get("/logs", elasticsearchQueryController.SearchDocumentLogs)
				})
			})

			// iam module
			r.Route("/iam", func(r chi.Router) {
				r.Post("/login", iamCommandController.LoginTenantUser)
				r.Post("/forgot-password", iamCommandController.ForgotTenantUserPassword)
				r.Post("/verify-email", iamCommandController.VerifyTenantUserEmail)
			})

			// inference module
			r.Route("/inference", func(r chi.Router) {
				// admin or owner only
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)
					r.Get("/quota", inferenceQueryController.GetInferenceQuota)

					r.Post("/onboarding-model-questionnaire-answers/add", inferenceCommandController.AddOnboardingModelQuestionnaireAnswers)
					r.Get("/onboarding-model-questionnaire-answers", inferenceQueryController.GetOnboardingModelQuestionnaireAnswers)

					r.Route("/model", func(r chi.Router) {
						r.Group(func(r chi.Router) {
							r.Use(iamMiddleware.RBACOwnerOrAdminGuard)

							// inference model routes
							r.Post("/add", inferenceCommandController.AddInferenceModel)
							r.Get("/list", inferenceQueryController.GetInferenceModels)
							r.Get("/{modelID}/feedback", inferenceQueryController.GetModelFeedbackByModelID)
							r.Delete("/{ID}/remove", inferenceCommandController.RemoveInferenceModel)
							r.Put("/{ID}/update", inferenceCommandController.UpdateInferenceModel)
							r.Put("/feedback/update", inferenceCommandController.UpdateModelFeedback)
							r.Delete("/{modelID}/feedback/remove", inferenceCommandController.RemoveModelFeedback)

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
							r.Post("/container/{containerID}/predict", inferenceCommandController.PredictInferenceModel)
							r.Get("/container/{containerID}/info", inferenceQueryController.GetInferenceModelInfo)
							r.Get("/container/{containerID}/facts", inferenceQueryController.GetInferenceModelFacts)
							r.Get("/available", inferenceQueryController.GetInferenceAvailableModels)
						})
					})

					// inference ingestion jobs
					r.Route("/ingestion", func(r chi.Router) {
						r.Group(func(r chi.Router) {
							r.Use(iamMiddleware.RBACOwnerOrAdminGuard)

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

					r.Group(func(r chi.Router) {
						r.Use(iamMiddleware.RBACOwnerOrAdminGuard)
						r.Get("/worklist/status", inferenceQueryController.GetWorklistStudyStatuses)
						r.Get("/worklist/events", inferenceQueryController.StreamWorklistEvents)
						r.Get("/worklist/studies/{studyInstanceUID}/runs", inferenceQueryController.GetStudyProcessingRunHistory)
						r.Get("/processing/runs/{runId}", inferenceQueryController.GetProcessingRunDetail)
						r.Post("/worklist/studies/{studyInstanceUID}/reprocess", inferenceCommandController.ReprocessStudy)
					})
				})
			})

			r.Route("/internal", func(r chi.Router) {
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

					// Thread management
					r.Post("/threads", orchestratorController.CreateThread)
					r.Get("/threads/{threadID}", orchestratorController.GetThread)

					// Message and payload management
					r.Post("/threads/{threadID}/chat", orchestratorController.CreateMessage)
					r.Post("/threads/{threadID}/dicom", orchestratorController.UploadDicomPayload)
				})
			})

			// orthanc module
			r.Route("/orthanc", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)

					r.Post("/modality/studies", orthancQueryController.FindModalityStudies)
					r.Post("/modality/retrieve", orthancCommandController.RetrieveModalityStudy)
					r.Post("/modality/{modalityID}/study/{studyInstanceUID}/series/store", orthancCommandController.StoreStudyCustomSeries)
					r.Get("/jobs", orthancQueryController.GetJobsInfo)
					r.Get("/modalities/list", orthancQueryController.ListDICOMModalities)
					r.Get("/modality/{modalityID}/linked/storage/enabled", orthancQueryController.GetLinkedDICOMModalityWithEnabledCStore)
					r.Get("/sop-instance/{sopInstanceUID}/find", orthancQueryController.FindLocalSOPInstance)

					// admin or owner only
					r.Group(func(r chi.Router) {
						r.Use(iamMiddleware.RBACOwnerOrAdminGuard)

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
					r.Put("/onboarding-consent/config/update", tenantCommandController.UpdateOnboardingConsentConfig)
					r.Put("/onboarding-registration/config/update", tenantCommandController.UpdateOnboardingRegistrationConfig)
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

					// admin or owner only
					r.Group(func(r chi.Router) {
						r.Use(iamMiddleware.RBACOwnerOrAdminGuard)

						r.Post("/add", userCommandController.CreateTenantUser)
						r.Post("/invite", userCommandController.SendTenantEmailInvite)
						r.Post("/invite/resend", userCommandController.ResendTenantEmailInvite)
						r.Get("/all", userQueryController.GetTenantUsers)
						r.Get("/invites", userQueryController.GetTenantUserEmailInvites)
						r.Get("/{ID}/policies/status", userQueryController.GetTenantUserPolicyStatus)
						r.Put("/update", userCommandController.UpdateTenantUser)
						r.Post("/{ID}/suspend", userCommandController.SuspendTenantUser)
						r.Post("/{ID}/reactivate", userCommandController.ReactivateTenantUser)
						r.Delete("/{ID}/remove", userCommandController.DeleteTenantUser)
						r.Delete("/invite/{ID}/remove", userCommandController.RemoveTenantUserEmailInvite)
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
