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
	"os"
	"strconv"
	"sync"

	"api-pacs/infrastructures/database/elasticsearch"
	elasticsearchTypes "api-pacs/infrastructures/database/elasticsearch/types"
	"api-pacs/infrastructures/database/redis"
	"api-pacs/infrastructures/providers/api/dockerinference"
	"api-pacs/infrastructures/providers/api/inference"
	"api-pacs/infrastructures/providers/api/kibana"
	"api-pacs/infrastructures/providers/api/orthanc"
	"api-pacs/infrastructures/providers/sdk/docker"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	"api-pacs/infrastructures/providers/sdk/mailgun"
	mailgunTypes "api-pacs/infrastructures/providers/sdk/mailgun/types"
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
	orthancService "api-pacs/module/orthanc/infrastructure/service"
	orthancREST "api-pacs/module/orthanc/interfaces/http/rest"
	predictionService "api-pacs/module/prediction/infrastructure/service"
	predictionREST "api-pacs/module/prediction/interfaces/http/rest"
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
	RegisterOrthancRESTCommandController() orthancREST.OrthancCommandController
	RegisterOrthancRESTQueryController() orthancREST.OrthancQueryController
	RegisterPredictionRESTCommandController() predictionREST.PredictionCommandController
	RegisterTenantRESTCommandController() tenantREST.TenantCommandController
	RegisterTenantRESTQueryController() tenantREST.TenantQueryController
	RegisterUserRESTCommandController() userREST.UserCommandController
	RegisterUserRESTQueryController() userREST.UserQueryController
}

type kernel struct{}

var (
	m                      sync.Mutex
	k                      *kernel
	containerOnce          sync.Once
	elasticsearchDBHandler *elasticsearch.ElasticsearchDBHandler
	redisIAMDBHandler      *redis.RedisDBHandler
	firebaseAdminSDK       *firebaseadmin.FirebaseAdminSDK
	orthancAPI             *orthanc.OrthancAPI
	kibanaAPI              *kibana.KibanaAPI
	inferenceAPI           *inference.InferenceAPI
	mailgunSDK             *mailgun.MailgunSDK
	dockerSDK              *docker.DockerSDK
	dockerInferenceAPI     *dockerinference.DockerInferenceAPI
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

	controller := iamREST.IAMCommandController{
		IAMCommandServiceInterface: service,
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
		InferenceQueryServiceInterface: service,
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

// RegisterPredictionRESTCommandController performs dependency injection to the RegisterPredictionRESTCommandController
func (k *kernel) RegisterPredictionRESTCommandController() predictionREST.PredictionCommandController {
	service := k.predictionCommandServiceContainer()

	controller := predictionREST.PredictionCommandController{
		PredictionCommandServiceInterface: service,
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

	controller := userREST.UserCommandController{
		UserCommandServiceInterface: service,
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

func OrthancCommandServiceDI() *orthancService.OrthancCommandService {
	m.Lock()
	defer m.Unlock()

	service := &orthancService.OrthancCommandService{
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
		MailgunSDK:                           mailgunSDK,
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
	commandRepository := &inferenceRepository.InferenceCommandRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	queryRepository := &inferenceRepository.InferenceQueryRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &inferenceService.InferenceCommandService{
		InferenceCommandRepositoryInterface: &inferenceRepository.InferenceCommandRepositoryCircuitBreaker{
			InferenceCommandRepositoryInterface: commandRepository,
		},
		InferenceQueryRepositoryInterface: &inferenceRepository.InferenceQueryRepositoryCircuitBreaker{
			InferenceQueryRepositoryInterface: queryRepository,
		},
		DockerSDKInterface:          dockerSDK,
		OrthancAPIInterface:         orthancAPI,
		DockerInferenceAPIInterface: dockerInferenceAPI,
	}

	return service
}

func (k *kernel) inferenceQueryServiceContainer() *inferenceService.InferenceQueryService {
	repository := &inferenceRepository.InferenceQueryRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &inferenceService.InferenceQueryService{
		InferenceQueryRepositoryInterface: &inferenceRepository.InferenceQueryRepositoryCircuitBreaker{
			InferenceQueryRepositoryInterface: repository,
		},
		DockerSDKInterface:          dockerSDK,
		DockerInferenceAPIInterface: dockerInferenceAPI,
	}

	return service
}

func (k *kernel) orthancCommandServiceContainer() *orthancService.OrthancCommandService {
	return OrthancCommandServiceDI()
}

func (k *kernel) predictionCommandServiceContainer() *predictionService.PredictionCommandService {
	service := &predictionService.PredictionCommandService{
		InferenceAPIInterface: inferenceAPI,
		OrthancAPIInterface:   orthancAPI,
	}

	return service
}

func (k *kernel) orthancQueryServiceContainer() *orthancService.OrthancQueryService {
	service := &orthancService.OrthancQueryService{
		OrthancAPIInterface:                  orthancAPI,
		TenantQueryServiceInterface:          k.tenantQueryServiceContainer(),
		ElasticsearchCommandServiceInterface: k.elasticsearchCommandServiceContainer(),
		UserQueryServiceInterface:            k.userQueryServiceContainer(),
	}

	return service
}

func (k *kernel) tenantCommandServiceContainer() *tenantService.TenantCommandService {
	repository := &tenantRepository.TenantCommandRepository{}

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

	service := &userService.UserCommandService{
		UserCommandRepositoryInterface: &userRepository.UserCommandRepositoryCircuitBreaker{
			UserCommandRepositoryInterface: repository,
		},
		UserQueryServiceInterface:            k.userQueryServiceContainer(),
		ElasticsearchCommandServiceInterface: k.elasticsearchCommandServiceContainer(),
		TenantQueryServiceInterface:          k.tenantQueryServiceContainer(),
		MailgunSDKInterface:                  mailgunSDK,
	}

	return service
}

func (k *kernel) userQueryServiceContainer() *userService.UserQueryService {
	repository := &userRepository.UserQueryRepository{
		FirebaseAdminSDK: firebaseAdminSDK,
	}

	service := &userService.UserQueryService{
		UserQueryRepositoryInterface: &userRepository.UserQueryRepositoryCircuitBreaker{
			UserQueryRepositoryInterface: repository,
		},
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

	// create new elasticsearch connection
	elasticsearchDBHandler, err = elasticsearch.NewTypedClient(elasticsearchTypes.Config{
		ElasticsearchURL: os.Getenv("ELASTICSEARCH_URL")})
	if err != nil {
		log.Fatalf("[SERVER] cannot create elasticsearch server: %v", err)
	}

	// init firebase admin sdk
	firebaseAdminSDK, err = firebaseadmin.NewApp(context.Background(), os.Getenv("FIREBASE_CONFIG_FILE_PATH"), os.Getenv("FIREBASE_PROJECT_ID"))
	if err != nil {
		log.Fatalf("[SERVER] cannot initialize firebase admin app: %v", err)
	}

	// init orthanc connection
	orthancAPI = orthanc.Init(os.Getenv("ORTHANC_BASE_URL"))

	// init inference connection
	inferenceAPI = inference.Init(os.Getenv("INFERENCE_BASE_URL"))

	// init kibana connection
	kibanaAPI = kibana.Init(os.Getenv("KIBANA_BASE_URL"))

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

	// run event listeners and cron jobs
	go RunOrthancLocalStudiesCacheHandler()
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
		})
	}
	return k
}
