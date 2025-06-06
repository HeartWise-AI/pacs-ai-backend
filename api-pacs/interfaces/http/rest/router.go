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
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"api-pacs/interfaces"
	"api-pacs/interfaces/http/rest/middlewares/cors"
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
	orthancProxy := interfaces.ServiceContainer().RegisterOrthancProxy()
	orthancCommandController := interfaces.ServiceContainer().RegisterOrthancRESTCommandController()
	orthancQueryController := interfaces.ServiceContainer().RegisterOrthancRESTQueryController()
	tenantQueryController := interfaces.ServiceContainer().RegisterTenantRESTQueryController()
	userCommandController := interfaces.ServiceContainer().RegisterUserRESTCommandController()
	userQueryController := interfaces.ServiceContainer().RegisterUserRESTQueryController()

	// create router
	r := chi.NewRouter()

	// global and recommended middlewares
	r.Use(middleware.RequestID)
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
				"version": "v0.20.1-beta",
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

		// orthanc openapi
		orthancDocsDir := http.Dir(filepath.Join(workDir, "docs", "orthanc"))
		FileServer(r, "/docs/orthanc", orthancDocsDir)
	})

	// API routes
	r.Group(func(r chi.Router) {
		r.Route("/v1", func(r chi.Router) {
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

					r.Route("/model", func(r chi.Router) {
						r.Group(func(r chi.Router) {
							r.Use(iamMiddleware.RBACOwnerOrAdminGuard)

							// inference model routes
							r.Post("/add", inferenceCommandController.AddInferenceModel)
							r.Get("/list", inferenceQueryController.GetInferenceModels)
							r.Delete("/{ID}/remove", inferenceCommandController.DeleteInferenceModel)
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
							r.Post("/container/{containerID}/predict", inferenceCommandController.PredictInferenceModel)
							r.Get("/container/{containerID}/info", inferenceQueryController.GetInferenceModelInfo)
							r.Get("/container/{containerID}/facts", inferenceQueryController.GetInferenceModelFacts)
							r.Get("/available", inferenceQueryController.GetInferenceAvailableModels)
						})
					})
				})
			})

			// lead module
			r.Route("/lead", func(r chi.Router) {
				r.Post("/contact-form", leadCommandController.AddContactForm)
				r.Post("/subscribe", leadCommandController.Subscribe)
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

					r.Get("/", tenantQueryController.GetTenantByID)
				})
			})

			// user module
			r.Route("/user", func(r chi.Router) {
				// superuser only
				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.FirebaseSuperUserGuard)

					r.Post("/owner/add", userCommandController.CreateTenantOwner)
				})

				r.Group(func(r chi.Router) {
					r.Use(iamMiddleware.TokenSessionAuthGuard)

					r.Get("/me", userQueryController.GetCurrentTenantUser)
					r.Put("/password/update", userCommandController.UpdateTenantUserPassword)

					// admin or owner only
					r.Group(func(r chi.Router) {
						r.Use(iamMiddleware.RBACOwnerOrAdminGuard)

						r.Post("/add", userCommandController.CreateTenantUser)
						r.Get("/all", userQueryController.GetTenantUsers)
						r.Get("/specialties", userQueryController.GetDoctorSpecialties)
						r.Put("/update", userCommandController.UpdateTenantUser)
						r.Delete("/{ID}/remove", userCommandController.DeleteTenantUser)
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

				r.Handle("/orthanc/dicom-web/*", orthancProxy.DICOMWebProxy())
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
	err := http.ListenAndServe(fmt.Sprintf(":%d", port), router.InitRouter())
	if err != nil {
		log.Fatalf("[SERVER] REST server failed %v", err)
	}
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
