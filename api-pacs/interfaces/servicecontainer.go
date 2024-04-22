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
	"log"
	"os"
	"sync"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
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
	// REST Controllers
	RegisterTenantRESTCommandController() tenantREST.TenantCommandController
	RegisterTenantRESTQueryController() tenantREST.TenantQueryController
	RegisterUserRESTCommandController() userREST.UserCommandController
	RegisterUserRESTQueryController() userREST.UserQueryController
}

type kernel struct{}

var (
	m                sync.Mutex
	k                *kernel
	containerOnce    sync.Once
	firebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
)

// ================================= REST ===================================
// Middlewares
// RegisterIAMRESTMiddleware performs dependency injection to the RegisterIAMRESTMiddleware
func (k *kernel) RegisterIAMRESTMiddleware() iamMiddleware.IAMMiddleware {
	middleware := iamMiddleware.IAMMiddleware{}
	return middleware
}

// Controllers
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
	repository := &tenantRepository.TenantQueryRepository{}

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

	// init firebase admin sdk
	firebaseAdminSDK, err = firebaseadmin.NewApp(context.Background(), os.Getenv("FIREBASE_CONFIG_FILE_PATH"), os.Getenv("FIREBASE_PROJECT_ID"))
	if err != nil {
		log.Fatalf("[SERVER] cannot initialize firebase admin app: %v", err)
	}
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
