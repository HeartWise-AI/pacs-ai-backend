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
)

// ServiceContainerInterface contains the dependency injected instances
type ServiceContainerInterface interface {
	// REST
	RegisterAuthRESTCommandController() authREST.AuthCommandController
	RegisterAuthRESTQueryController() authREST.AuthQueryController
}

type kernel struct{}

var (
	m                sync.Mutex
	k                *kernel
	containerOnce    sync.Once
	firebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
)

// ================================= REST ===================================
// RegisterAuthRESTCommandController performs dependency injection to the RegisterAuthRESTCommandController
func (k *kernel) RegisterAuthRESTCommandController() authREST.AuthCommandController {
	service := k.authCommandServiceContainer()

	controller := authREST.AuthCommandController{
		AuthCommandServiceInterface: service,
	}

	return controller
}

// RegisterAuthRESTQueryController performs dependency injection to the RegisterAuthRESTQueryController
func (k *kernel) RegisterAuthRESTQueryController() authREST.AuthQueryController {
	service := k.authQueryServiceContainer()

	controller := authREST.AuthQueryController{
		AuthQueryServiceInterface: service,
	}

	return controller
}

//==========================================================================

func (k *kernel) authCommandServiceContainer() *authService.AuthCommandService {
	repository := &authRepository.AuthCommandRepository{
		MySQLDBHandlerInterface: mysqlDBHandler,
	}

	service := &authService.AuthCommandService{
		AuthCommandRepositoryInterface: &authRepository.AuthCommandRepositoryCircuitBreaker{
			AuthCommandRepositoryInterface: repository,
		},
	}

	return service
}

func (k *kernel) authQueryServiceContainer() *authService.AuthQueryService {
	repository := &authRepository.AuthQueryRepository{
		MySQLDBHandlerInterface: mysqlDBHandler,
	}

	service := &authService.AuthQueryService{
		AuthQueryRepositoryInterface: &authRepository.AuthQueryRepositoryCircuitBreaker{
			AuthQueryRepositoryInterface: repository,
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
