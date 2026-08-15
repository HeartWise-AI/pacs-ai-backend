/*
|--------------------------------------------------------------------------
| Service Container
|--------------------------------------------------------------------------
|
| This file performs the compiled dependency injection for your middlewares,
| controllers, services, providers, repositories, etc..
|
*/
package interfaces

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-pacs/infrastructures/database/elasticsearch"
	elasticsearchTypes "api-pacs/infrastructures/database/elasticsearch/types"
	"api-pacs/infrastructures/database/postgresql"
	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/infrastructures/database/redis"
	cloudflare "api-pacs/infrastructures/providers/api/cloudflare"
	"api-pacs/infrastructures/providers/api/dockerinference"
	"api-pacs/infrastructures/providers/api/docusign"
	docusignTypes "api-pacs/infrastructures/providers/api/docusign/types"
	"api-pacs/infrastructures/providers/api/identitytoolkit"
	"api-pacs/infrastructures/providers/api/kibana"
	"api-pacs/infrastructures/providers/api/mailchimp"
	mailchimpTypes "api-pacs/infrastructures/providers/api/mailchimp/types"
	"api-pacs/infrastructures/providers/api/orthanc"
	"api-pacs/infrastructures/providers/sdk/docker"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	"api-pacs/infrastructures/providers/sdk/mailgun"
	mailgunTypes "api-pacs/infrastructures/providers/sdk/mailgun/types"
	"api-pacs/interfaces/http/rest/clientip"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	dockerInferenceProxy "api-pacs/interfaces/http/rest/proxies/dockerinference"
	orthancProxy "api-pacs/interfaces/http/rest/proxies/orthanc"
	elasticsearchRepository "api-pacs/module/elasticsearch/infrastructure/repository"
	elasticsearchService "api-pacs/module/elasticsearch/infrastructure/service"
	elasticsearchREST "api-pacs/module/elasticsearch/interfaces/http/rest"
	iamRepository "api-pacs/module/iam/infrastructure/repository"
	iamService "api-pacs/module/iam/infrastructure/service"
	iamREST "api-pacs/module/iam/interfaces/http/rest"
	inferenceRepository "api-pacs/module/inference/infrastructure/repository"
	inferenceService "api-pacs/module/inference/infrastructure/service"
	inferenceREST "api-pacs/module/inference/interfaces/http/rest"
	leadService "api-pacs/module/lead/infrastructure/service"
	leadREST "api-pacs/module/lead/interfaces/http/rest"
	"api-pacs/module/orchestrator/domain/entity"
	orchestratorService "api-pacs/module/orchestrator/infrastructure/service"
	orchestratorREST "api-pacs/module/orchestrator/interfaces/http/rest"
	orthancRepository "api-pacs/module/orthanc/infrastructure/repository"
	orthancService "api-pacs/module/orthanc/infrastructure/service"
	orthancREST "api-pacs/module/orthanc/interfaces/http/rest"
	tenantRepository "api-pacs/module/tenant/infrastructure/repository"
	tenantService "api-pacs/module/tenant/infrastructure/service"
	tenantREST "api-pacs/module/tenant/interfaces/http/rest"
	userRepository "api-pacs/module/user/infrastructure/repository"
	userService "api-pacs/module/user/infrastructure/service"
	userREST "api-pacs/module/user/interfaces/http/rest"
)

// ServiceContainerInterface contains the dependency injected instances
type ServiceContainerInterface interface {
	// REST Middlewares
	RegisterIAMRESTMiddleware() iamMiddleware.IAMMiddleware
	// REST Proxies
	RegisterDockerInferenceProxy() dockerInferenceProxy.DockerInferenceProxy
	RegisterOrthancProxy() orthancProxy.OrthancProxy
	// REST Controllers
	RegisterElasticsearchRESTCommandController() elasticsearchREST.ElasticsearchCommandController
	RegisterElasticsearchRESTQueryController() elasticsearchREST.ElasticsearchQueryController
	RegisterIAMRESTCommandController() iamREST.IAMCommandController
	RegisterInferenceRESTCommandController() inferenceREST.InferenceCommandController
	RegisterInferenceRESTQueryController() inferenceREST.InferenceQueryController
	RegisterLeadRESTCommandController() leadREST.LeadCommandController
	RegisterOrchestratorRESTController() orchestratorREST.OrchestratorController
	RegisterOrthancRESTCommandController() orthancREST.OrthancCommandController
	RegisterOrthancRESTQueryController() orthancREST.OrthancQueryController
	RegisterTenantRESTCommandController() tenantREST.TenantCommandController
	RegisterTenantRESTQueryController() tenantREST.TenantQueryController
	RegisterUserRESTCommandController() userREST.UserCommandController
	RegisterUserRESTQueryController() userREST.UserQueryController
}

type kernel struct{}

var (
	m                          sync.Mutex
	k                          *kernel
	containerOnce              sync.Once
	elasticsearchDBHandler     *elasticsearch.ElasticsearchDBHandler
	redisIAMDBHandler          *redis.RedisDBHandler
	postgresqlDBHanddler       *postgresql.PostgreSQLDBHandler
	firebaseAdminSDK           *firebaseadmin.FirebaseAdminSDK
	orthancAPI                 *orthanc.OrthancAPI
	kibanaAPI                  *kibana.KibanaAPI
	mailchimpAPI               *mailchimp.MailchimpAPI
	cloudflareAPI              *cloudflare.CloudflareAPI
	identityToolkitAPI         *identitytoolkit.IdentityToolkitAPI
	mailgunSDK                 *mailgun.MailgunSDK
	dockerSDK                  *docker.DockerSDK
	dockerInferenceAPI         *dockerinference.DockerInferenceAPI
	docusignAPI                *docusign.DocusignAPI
	worklistNotificationBroker *inferenceService.RedisWorklistNotificationBroker
	inferenceQuotaManager      *inferenceService.RedisInferenceQuotaManager
)

// ================================= REST ===================================
// Middlewares
// RegisterIAMRESTMiddleware performs dependency injection to the RegisterIAMRESTMiddleware
func (k *kernel) RegisterIAMRESTMiddleware() iamMiddleware.IAMMiddleware {
	middleware := iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: k.iamCommandServiceContainer(),
		IAMQueryServiceInterface:   k.iamQueryServiceContainer(),
	}

	return middleware
}

// RegisterLeadRESTCommandController performs dependency injection to the RegisterLeadRESTCommandController
func (k *kernel) RegisterLeadRESTCommandController() leadREST.LeadCommandController {
	service := k.leadCommandServiceContainer()

	controller := leadREST.LeadCommandController{
		LeadCommandServiceInterface: service,
	}

	return controller
}

// Proxies
// RegisterDockerInferenceProxy performs dependency injection to the RegisterDockerInferenceProxy
func (k *kernel) RegisterDockerInferenceProxy() dockerInferenceProxy.DockerInferenceProxy {
	return dockerInferenceProxy.DockerInferenceProxy{}
}

// RegisterOrthancProxy performs dependency injection to the RegisterOrthancProxy
func (k *kernel) RegisterOrthancProxy() orthancProxy.OrthancProxy {
	return orthancProxy.OrthancProxy{}
}

// Controllers

// RegisterElasticsearchRESTCommandController performs dependency injection to the RegisterElasticsearchRESTCommandController
func (k *kernel) RegisterElasticsearchRESTCommandController() elasticsearchREST.ElasticsearchCommandController {
	service := k.elasticsearchCommandServiceContainer()

	controller := elasticsearchREST.ElasticsearchCommandController{
		ElasticsearchCommandServiceInterface: service,
	}

	return controller
}

// RegisterElasticsearchRESTQueryController performs dependency injection to the RegisterElasticsearchRESTQueryController
func (k *kernel) RegisterElasticsearchRESTQueryController() elasticsearchREST.ElasticsearchQueryController {
	service := k.elasticsearchQueryServiceContainer()

	controller := elasticsearchREST.ElasticsearchQueryController{
		ElasticsearchQueryServiceInterface: service,
	}

	return controller
}

// RegisterIAMRESTCommandController performs dependency injection to the RegisterIAMRESTCommandController
func (k *kernel) RegisterIAMRESTCommandController() iamREST.IAMCommandController {
	service := k.iamCommandServiceContainer()
	trustedProxyCIDRs, err := clientip.ParseTrustedProxyCIDRs(os.Getenv("LOGIN_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		log.Printf("[security] event=login_trusted_proxy_config_invalid err=%v", err)
	}

	controller := iamREST.IAMCommandController{
		IAMCommandServiceInterface: service,
		TrustedProxyCIDRs:          trustedProxyCIDRs,
	}

	return controller
}

// RegisterInferenceRESTCommandController performs dependency injection to the RegisterInferenceRESTCommandController
func (k *kernel) RegisterInferenceRESTCommandController() inferenceREST.InferenceCommandController {
	service := k.inferenceCommandServiceContainer()

	controller := inferenceREST.InferenceCommandController{
		InferenceCommandServiceInterface: service,
	}

	return controller
}

// RegisterInferenceRESTQueryController performs dependency injection to the RegisterInferenceRESTQueryController
func (k *kernel) RegisterInferenceRESTQueryController() inferenceREST.InferenceQueryController {
	service := k.inferenceQueryServiceContainer()

	controller := inferenceREST.InferenceQueryController{
		InferenceQueryServiceInterface:          service,
		WorklistNotificationSubscriberInterface: worklistNotificationBroker,
		WorklistEventHeartbeatInterval:          20 * time.Second,
	}

	return controller
}

// RegisterOrchestratorRESTController performs dependency injection to the RegisterOrchestratorRESTController
func (k *kernel) RegisterOrchestratorRESTController() orchestratorREST.OrchestratorController {
	service := k.orchestratorServiceContainer()

	controller := orchestratorREST.OrchestratorController{
		OrchestratorServiceInterface: service,
	}

	return controller
}

// RegisterOrthancRESTCommandController performs dependency injection to the RegisterOrthancRESTCommandController
func (k *kernel) RegisterOrthancRESTCommandController() orthancREST.OrthancCommandController {
	service := k.orthancCommandServiceContainer()

	controller := orthancREST.OrthancCommandController{
		OrthancCommandServiceInterface: service,
	}

	return controller
}

// RegisterOrthancRESTQueryController performs dependency injection to the RegisterOrthancRESTQueryController
func (k *kernel) RegisterOrthancRESTQueryController() orthancREST.OrthancQueryController {
	service := k.orthancQueryServiceContainer()

	controller := orthancREST.OrthancQueryController{
		OrthancQueryServiceInterface: service,
	}

	return controller
}

// RegisterTenantRESTCommandController performs dependency injection to the RegisterTenantRESTCommandController
func (k *kernel) RegisterTenantRESTCommandController() tenantREST.TenantCommandController {
	service := k.tenantCommandServiceContainer()

	controller := tenantREST.TenantCommandController{
		TenantCommandServiceInterface: service,
	}

	return controller
}

// RegisterTenantRESTQueryController performs dependency injection to the RegisterTenantRESTQueryController
func (k *kernel) RegisterTenantRESTQueryController() tenantREST.TenantQueryController {
	service := k.tenantQueryServiceContainer()

	controller := tenantREST.TenantQueryController{
		TenantQueryServiceInterface: service,
	}

	return controller
}

// RegisterUserRESTCommandController performs dependency injection to the RegisterUserRESTCommandController
func (k *kernel) RegisterUserRESTCommandController() userREST.UserCommandController {
	service := k.userCommandServiceContainer()
	trustedProxyCIDRs, err := userREST.ParseTrustedProxyCIDRs(os.Getenv("REGISTRATION_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		log.Printf("[security] event=registration_trusted_proxy_config_invalid err=%v", err)
	}

	controller := userREST.UserCommandController{
		UserCommandServiceInterface: service,
		TrustedProxyCIDRs:           trustedProxyCIDRs,
	}

	return controller
}

// RegisterUserRESTQueryController performs dependency injection to the RegisterUserRESTQueryController
func (k *kernel) RegisterUserRESTQueryController() userREST.UserQueryController {
	service := k.userQueryServiceContainer()

	controller := userREST.UserQueryController{
		UserQueryServiceInterface: service,
	}

	return controller
}

// ==========================================================================

func InferenceCommandServiceDI() *inferenceService.InferenceCommandService {
	commandRepository := &inferenceRepository.InferenceCommandRepository{
		FirebaseAdminSDK:              firebaseAdminSDK,
		PostgresSQLDBHandlerInterface: postgresqlDBHanddler,
	}

	queryRepository := &inferenceRepository.InferenceQueryRepository{
		FirebaseAdminSDK:              firebaseAdminSDK,
		PostgresSQLDBHandlerInterface: postgresqlDBHanddler,
	}

	processingRunRepository := &inferenceRepository.InferenceProcessingRunRepository{
		PostgresSQLDBHandlerInterface: postgresqlDBHanddler,
	}

	service := &inferenceService.InferenceCommandService{
		InferenceCommandRepositoryInterface: &inferenceRepository.InferenceCommandRepositoryCircuitBreaker{
			InferenceCommandRepositoryInterface: commandRepository,
		},
		InferenceQueryRepositoryInterface: &inferenceRepository.InferenceQueryRepositoryCircuitBreaker{
			InferenceQueryRepositoryInterface: queryRepository,
		},
		InferenceProcessingRunRepositoryInterface: &inferenceRepository.InferenceProcessingRunRepositoryCircuitBreaker{
			InferenceProcessingRunRepositoryInterface: processingRunRepository,
		},
		TenantQueryServiceInterface:             k.tenantQueryServiceContainer(),
		UserQueryServiceInterface:               k.userQueryServiceContainer(),
		OrthancCommandServiceInterface:          k.orthancCommandServiceContainer(),
		OrthancQueryServiceInterface:            k.orthancQueryServiceContainer(),
		ElasticsearchCommandServiceInterface:    k.elasticsearchCommandServiceContainer(),
		DockerSDKInterface:                      dockerSDK,
		OrthancAPIInterface:                     orthancAPI,
		DockerInferenceAPIInterface:             dockerInferenceAPI,
		InferenceQuotaManagerInterface:          inferenceQuotaManager,
		StudyServiceDispatchSemaphore:           make(chan struct{}, configuredStudyServiceDispatchConcurrency()),
		WorklistNotificationPublisherInterface:  worklistNotificationBroker,
		ProcessingReconciliationMetricsRecorder: &inferenceService.LoggingProcessingReconciliationMetricsRecorder{},
		RequireProcessingRunID:                  configuredProcessingRunIDRequirement(),
		ProcessingDispatcherInterface: &inferenceService.StudyServiceDispatcher{
			StudyServiceBaseURL:       os.Getenv("STUDY_SERVICE_BASE_URL"),
			StudyServiceIngestToken:   os.Getenv("STUDY_SERVICE_INGEST_TOKEN"),
			StudyServiceOperatorToken: os.Getenv("STUDY_SERVICE_OPERATOR_TOKEN"),
			StudyServiceClient:        &http.Client{Timeout: 5 * time.Second},
			OrthancAPIInterface:       orthancAPI,
		},
	}

	return service
}

func configuredProcessingRunIDRequirement() bool {
	required, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("INFERENCE_REQUIRE_PROCESSING_RUN_ID")))
	return err == nil && required
}

func configuredStudyServiceDispatchConcurrency() int {
	const defaultConcurrency = 16

	value := strings.TrimSpace(os.Getenv("STUDY_SERVICE_DISPATCH_CONCURRENCY"))
	if value == "" {
		return defaultConcurrency
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return defaultConcurrency
	}

	return parsed
}

func OrthancCommandServiceDI() *orthancService.OrthancCommandService {
	m.Lock()
	defer m.Unlock()

	repository := &orthancRepository.OrthancCommandRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &orthancService.OrthancCommandService{
		OrthancCommandRepositoryInterface: &orthancRepository.OrthancCommandRepositoryCircuitBreaker{
			OrthancCommandRepositoryInterface: repository,
		},
		OrthancAPIInterface:                  orthancAPI,
		TenantQueryServiceInterface:          k.tenantQueryServiceContainer(),
		ElasticsearchCommandServiceInterface: k.elasticsearchCommandServiceContainer(),
		UserQueryServiceInterface:            k.userQueryServiceContainer(),
	}

	return service
}

func (k *kernel) elasticsearchCommandServiceContainer() *elasticsearchService.ElasticsearchCommandService {
	queryRepository := &elasticsearchRepository.ElasticsearchQueryRepository{
		ElasticsearchDBHandlerInterface: elasticsearchDBHandler,
	}

	repository := &elasticsearchRepository.ElasticsearchCommandRepository{
		ElasticsearchDBHandlerInterface: elasticsearchDBHandler,
	}

	service := &elasticsearchService.ElasticsearchCommandService{
		ElasticsearchCommandRepositoryInterface: &elasticsearchRepository.ElasticsearchCommandRepositoryCircuitBreaker{
			ElasticsearchCommandRepositoryInterface: repository,
		},
		ElasticsearchQueryRepositoryInterface: &elasticsearchRepository.ElasticsearchQueryRepositoryCircuitBreaker{
			ElasticsearchQueryRepositoryInterface: queryRepository,
		},
		KibanaAPIInterface: kibanaAPI,
	}

	return service
}

func (k *kernel) elasticsearchQueryServiceContainer() *elasticsearchService.ElasticsearchQueryService {
	repository := &elasticsearchRepository.ElasticsearchQueryRepository{
		ElasticsearchDBHandlerInterface: elasticsearchDBHandler,
	}

	service := &elasticsearchService.ElasticsearchQueryService{
		ElasticsearchQueryRepositoryInterface: &elasticsearchRepository.ElasticsearchQueryRepositoryCircuitBreaker{
			ElasticsearchQueryRepositoryInterface: repository,
		},
	}

	return service
}

func (k *kernel) iamCommandServiceContainer() *iamService.IAMCommandService {
	repository := &iamRepository.IAMCommandRepository{
		RedisDBHandlerInterface: redisIAMDBHandler,
	}

	service := &iamService.IAMCommandService{
		IAMCommandRepositoryInterface: &iamRepository.IAMCommandRepositoryCircuitBreaker{
			IAMCommandRepositoryInterface: repository,
		},
		UserQueryServiceInterface:            k.userQueryServiceContainer(),
		ElasticsearchCommandServiceInterface: k.elasticsearchCommandServiceContainer(),
		TenantQueryServiceInterface:          k.tenantQueryServiceContainer(),
		FirebaseAdminSDK:                     firebaseAdminSDK,
		TenantIDTokenVerifierInterface:       firebaseAdminSDK,
		IdentityToolkitAPIInterface:          identityToolkitAPI,
		LoginTurnstileAPIInterface:           cloudflareAPI,
		LoginAbuseProtectionInterface: &iamService.RedisLoginAbuseProtection{
			RedisDBHandlerInterface: redisIAMDBHandler,
			Config:                  iamService.LoginAbuseProtectionConfigFromEnvironment(),
		},
		LoginTurnstileAllowedHostnames: iamService.LoginTurnstileAllowedHostnamesFromEnvironment(
			os.Getenv("LOGIN_TURNSTILE_ALLOWED_HOSTNAMES"),
		),
		MailgunSDK: mailgunSDK,
	}

	return service
}

func (k *kernel) iamQueryServiceContainer() *iamService.IAMQueryService {
	repository := &iamRepository.IAMQueryRepository{
		RedisDBHandlerInterface: redisIAMDBHandler,
	}

	service := &iamService.IAMQueryService{
		IAMQueryRepositoryInterface: &iamRepository.IAMQueryRepositoryCircuitBreaker{
			IAMQueryRepositoryInterface: repository,
		},
	}

	return service
}

func (k *kernel) inferenceCommandServiceContainer() *inferenceService.InferenceCommandService {
	return InferenceCommandServiceDI()
}

func (k *kernel) inferenceQueryServiceContainer() *inferenceService.InferenceQueryService {
	repository := &inferenceRepository.InferenceQueryRepository{
		FirebaseAdminSDK:              firebaseAdminSDK,
		PostgresSQLDBHandlerInterface: postgresqlDBHanddler,
	}
	processingRunRepository := &inferenceRepository.InferenceProcessingRunRepository{
		PostgresSQLDBHandlerInterface: postgresqlDBHanddler,
	}

	service := &inferenceService.InferenceQueryService{
		InferenceQueryRepositoryInterface: &inferenceRepository.InferenceQueryRepositoryCircuitBreaker{
			InferenceQueryRepositoryInterface: repository,
		},
		InferenceProcessingRunRepositoryInterface: &inferenceRepository.InferenceProcessingRunRepositoryCircuitBreaker{
			InferenceProcessingRunRepositoryInterface: processingRunRepository,
		},
		DockerSDKInterface:             dockerSDK,
		DockerInferenceAPIInterface:    dockerInferenceAPI,
		InferenceQuotaManagerInterface: inferenceQuotaManager,
	}

	return service
}

func (k *kernel) leadCommandServiceContainer() *leadService.LeadCommandService {
	service := &leadService.LeadCommandService{
		MailchimpAPIInterface:  mailchimpAPI,
		CloudflareAPIInterface: cloudflareAPI,
	}

	return service
}

func (k *kernel) leadQueryServiceContainer() *leadService.LeadQueryService {
	service := &leadService.LeadQueryService{}

	return service
}

func (k *kernel) orthancCommandServiceContainer() *orthancService.OrthancCommandService {
	return OrthancCommandServiceDI()
}

func (k *kernel) orthancQueryServiceContainer() *orthancService.OrthancQueryService {
	repository := &orthancRepository.OrthancQueryRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &orthancService.OrthancQueryService{
		OrthancQueryRepositoryInterface: &orthancRepository.OrthancQueryRepositoryCircuitBreaker{
			OrthancQueryRepositoryInterface: repository,
		},
		OrthancAPIInterface:                  orthancAPI,
		TenantQueryServiceInterface:          k.tenantQueryServiceContainer(),
		ElasticsearchCommandServiceInterface: k.elasticsearchCommandServiceContainer(),
		UserQueryServiceInterface:            k.userQueryServiceContainer(),
	}

	return service
}

// orchestratorServiceContainer returns the orchestrator service with dependencies
func (k *kernel) orchestratorServiceContainer() *orchestratorService.OrchestratorService {
	// Configure orchestrator service
	orchestratorAPIURL := os.Getenv("ORCHESTRATOR_API_URL")

	service := &orchestratorService.OrchestratorService{
		OrchestratorAPIURL: orchestratorAPIURL,
		OrchestratorClient: &http.Client{Timeout: 5 * time.Minute},
		ThreadsByID:        make(map[string]*entity.Thread),
	}

	return service
}

func (k *kernel) tenantCommandServiceContainer() *tenantService.TenantCommandService {
	repository := &tenantRepository.TenantCommandRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &tenantService.TenantCommandService{
		TenantCommandRepositoryInterface: &tenantRepository.TenantCommandRepositoryCircuitBreaker{
			TenantCommandRepositoryInterface: repository,
		},
	}

	return service
}

func (k *kernel) tenantQueryServiceContainer() *tenantService.TenantQueryService {
	repository := &tenantRepository.TenantQueryRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &tenantService.TenantQueryService{
		TenantQueryRepositoryInterface: &tenantRepository.TenantQueryRepositoryCircuitBreaker{
			TenantQueryRepositoryInterface: repository,
		},
	}

	return service
}

func (k *kernel) userCommandServiceContainer() *userService.UserCommandService {
	repository := &userRepository.UserCommandRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	queryRepository := &userRepository.UserQueryRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &userService.UserCommandService{
		CloudflareAPIInterface: cloudflareAPI,
		RegistrationRateLimiterInterface: &userService.RedisRegistrationRateLimiter{
			RedisDBHandlerInterface: redisIAMDBHandler,
			Config:                  userService.RegistrationRateLimitConfigFromEnvironment(),
		},
		UserCommandRepositoryInterface: &userRepository.UserCommandRepositoryCircuitBreaker{
			UserCommandRepositoryInterface: repository,
		},
		UserQueryRepositoryInterface: &userRepository.UserQueryRepositoryCircuitBreaker{
			UserQueryRepositoryInterface: queryRepository,
		},
		TenantCommandServiceInterface:        k.tenantCommandServiceContainer(),
		TenantQueryServiceInterface:          k.tenantQueryServiceContainer(),
		InferenceCommandServiceInterface:     k.inferenceCommandServiceContainer(),
		InferenceQueryServiceInterface:       k.inferenceQueryServiceContainer(),
		ElasticsearchCommandServiceInterface: k.elasticsearchCommandServiceContainer(),
		IAMCommandServiceInterface:           k.iamCommandServiceContainer(),
		MailgunSDKInterface:                  mailgunSDK,
	}

	return service
}

func (k *kernel) userQueryServiceContainer() *userService.UserQueryService {
	repository := &userRepository.UserQueryRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	commandRepository := &userRepository.UserCommandRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &userService.UserQueryService{
		UserQueryRepositoryInterface: &userRepository.UserQueryRepositoryCircuitBreaker{
			UserQueryRepositoryInterface: repository,
		},
		UserCommandRepositoryInterface: &userRepository.UserCommandRepositoryCircuitBreaker{
			UserCommandRepositoryInterface: commandRepository,
		},
		TenantQueryServiceInterface:          k.tenantQueryServiceContainer(),
		ElasticsearchCommandServiceInterface: k.elasticsearchCommandServiceContainer(),
		DocusignAPIInterface:                 docusignAPI,
	}

	return service
}

func registerHandlers() {
	var err error

	// create new redis connection
	redisIAMDBHandler = &redis.RedisDBHandler{}

	redisIAMDB, _ := strconv.Atoi(os.Getenv("REDIS_IAM_DB"))
	_, err = redisIAMDBHandler.Connect(fmt.Sprintf("%s:%s", os.Getenv("REDIS_HOST"), os.Getenv("REDIS_PORT")), os.Getenv("REDIS_PASSWORD"), redisIAMDB)
	if err != nil {
		log.Fatalf("[SERVER] cannot connect to account redis IAM server %v", err)
	}
	inferenceQuotaManager = &inferenceService.RedisInferenceQuotaManager{
		Client: redisIAMDBHandler.Client,
		Config: inferenceService.InferenceQuotaConfigFromEnvironment(),
	}
	worklistNotificationBroker = inferenceService.NewRedisWorklistNotificationBroker(
		&inferenceService.RedisWorklistNotificationTransport{Client: redisIAMDBHandler.Client},
	)
	if err = worklistNotificationBroker.Start(context.Background()); err != nil {
		log.Fatalf("[SERVER] cannot subscribe to worklist Redis events %v", err)
	}

	// create new elasticsearch connection
	elasticsearchDBHandler, err = elasticsearch.NewTypedClient(elasticsearchTypes.Config{
		ElasticsearchURL: os.Getenv("ELASTICSEARCH_URL")})
	if err != nil {
		log.Fatalf("[SERVER] cannot create elasticsearch server: %v", err)
	}

	// create new postgresql connection
	postgresqlDBHanddler = &postgresql.PostgreSQLDBHandler{}

	err = postgresqlDBHanddler.Connect(postgresqlTypes.ConnectionParams{
		DBHost:     os.Getenv("POSTGRES_DB_HOST"),
		DBPort:     os.Getenv("POSTGRES_DB_PORT"),
		DBDatabase: os.Getenv("POSTGRES_DB_DATABASE"),
		DBUsername: os.Getenv("POSTGRES_DB_USERNAME"),
		DBPassword: os.Getenv("POSTGRES_DB_PASSWORD"),
	})
	if err != nil {
		log.Fatalf("[SERVER] cannot connect to postgresql server: %v", err)
	}

	// init firebase admin sdk
	firebaseAdminSDK, err = firebaseadmin.NewApp(context.Background(), os.Getenv("FIREBASE_CONFIG_FILE_PATH"), os.Getenv("FIREBASE_PROJECT_ID"))
	if err != nil {
		log.Fatalf("[SERVER] cannot initialize firebase admin app: %v", err)
	}

	// init orthanc connection
	orthancAPI = orthanc.Init(os.Getenv("ORTHANC_BASE_URL"))

	// init kibana connection
	kibanaAPI = kibana.Init(os.Getenv("KIBANA_BASE_URL"))

	// init mailchimp API
	mailchimpAPI = mailchimp.Init(mailchimpTypes.Config{
		BaseURL: os.Getenv("MAILCHIMP_BASE_URL"),
		APIKey:  os.Getenv("MAILCHIMP_API_KEY"),
		ListID:  os.Getenv("MAILCHIMP_LIST_ID"),
	})

	// init cloudflare API
	cloudflareAPI = cloudflare.Init(os.Getenv("CLOUDFLARE_SECRET_KEY"))

	// init Firebase Identity Toolkit password authentication API
	identityToolkitAPI = identitytoolkit.Init(os.Getenv("FIREBASE_WEB_API_KEY"))

	// init mailgun sdk
	mailgunSDK, err = mailgun.NewMailgun(mailgunTypes.Config{
		APIKey: os.Getenv("MAILGUN_API_KEY"),
		Domain: os.Getenv("MAILGUN_DOMAIN"),
	})
	if err != nil {
		log.Fatalf("[SERVER] cannot initialize mailgun: %v", err)
	}

	// init docker sdk
	dockerSDK, err = docker.NewClient(dockerTypes.Config{
		Username: os.Getenv("DOCKER_USERNAME"),
		Password: os.Getenv("DOCKER_PASSWORD"),
		Network:  os.Getenv("DOCKER_NETWORK"),
	})
	if err != nil {
		log.Fatalf("[SERVER] cannot initialize docker: %v", err)
	}

	// init docker inference api
	dockerInferenceAPI = &dockerinference.DockerInferenceAPI{}

	// init docusign API
	docusignAPI = docusign.Init(docusignTypes.Credential{
		IntegrationKey: os.Getenv("DOCUSIGN_INTEGRATION_KEY"),
		UserID:         os.Getenv("DOCUSIGN_USER_ID"),
		AccountBaseURI: os.Getenv("DOCUSIGN_ACCOUNT_BASE_URI"),
		AuthServer:     os.Getenv("DOCUSIGN_AUTH_SERVER"),
		PrivateKey:     os.Getenv("DOCUSIGN_PRIVATE_KEY"),
		AccountID:      os.Getenv("DOCUSIGN_ACCOUNT_ID"),
	})
}

// ServiceContainer export instantiated service container once
func ServiceContainer() ServiceContainerInterface {
	m.Lock()
	defer m.Unlock()

	if k == nil {
		containerOnce.Do(func() {
			// register container handlers
			registerHandlers()

			k = &kernel{}

			// run event listeners and cron jobs after all handlers are registered
			go RunInferenceIngestionServiceHandler()
			go RunInferenceIngestionRetrievalWorkerHandler()
			go RunInferenceIngestionReconciliationWorkerHandler()
			go RunOrthancLocalStudiesCacheHandler()
		})
	}
	return k
}
