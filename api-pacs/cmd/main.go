/*
|--------------------------------------------------------------------------
| Main
|--------------------------------------------------------------------------
|
| This is the entry point for listeners of the project.
| You can create and run goroutines for event listeners below before the HTTP listener.
|
*/
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"

	"api-pacs/infrastructures/database/postgresql"
	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/interfaces/http/rest"
	orthancRepository "api-pacs/module/orthanc/infrastructure/repository"
	orthancService "api-pacs/module/orthanc/infrastructure/service"
)

const defaultAppTimezone = "America/Toronto"

func init() {
	// Local development may use .env; production injects the environment.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		panic(err)
	}
}

func main() {
	// One-off admin command, run in the api-pacs container:
	//   api-pacs backfill-orthanc-tenant-studies [--dry-run]
	// assigns every study in the shared Orthanc to its tenant and prints a report.
	if len(os.Args) > 1 && os.Args[1] == "backfill-orthanc-tenant-studies" {
		os.Exit(backfillOrthancTenantStudies(len(os.Args) > 2 && os.Args[2] == "--dry-run"))
	}

	location, err := time.LoadLocation(configuredTimezone())
	if err != nil {
		log.Fatalf("[SERVER] Invalid timezone: %v", err)
	}
	time.Local = location

	// rest port
	restPort, err := strconv.Atoi(os.Getenv("API_URL_REST_PORT"))
	if err != nil {
		log.Fatalf("[SERVER] Invalid port")
	}
	if len(fmt.Sprintf("%d", restPort)) == 0 {
		restPort = 8000 // default grpcPort is 8000 if not set
	}

	// serve rest server
	rest.ChiRouter().Serve(restPort)
}

func configuredTimezone() string {
	value := os.Getenv("APP_TIMEZONE")
	if value == "" {
		return defaultAppTimezone
	}

	return value
}

func backfillOrthancTenantStudies(dryRun bool) int {
	database := &postgresql.PostgreSQLDBHandler{}
	if err := database.Connect(postgresqlTypes.ConnectionParams{
		DBHost:     os.Getenv("POSTGRES_DB_HOST"),
		DBPort:     os.Getenv("POSTGRES_DB_PORT"),
		DBDatabase: os.Getenv("POSTGRES_DB_DATABASE"),
		DBUsername: os.Getenv("POSTGRES_DB_USERNAME"),
		DBPassword: os.Getenv("POSTGRES_DB_PASSWORD"),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "cannot connect to postgresql:", err)
		return 1
	}
	sync := &orthancService.TenantStudySync{
		Store: &orthancRepository.OrthancTenantStudyRepository{
			PostgresSQLDBHandlerInterface: database,
			DefaultTenantID:               orthancRepository.DefaultTenantID(),
		},
		OrthancBaseURL:  os.Getenv("ORTHANC_BASE_URL"),
		DefaultTenantID: orthancRepository.DefaultTenantID(),
		DryRun:          dryRun,
	}
	report, err := sync.Backfill(context.Background())
	out, _ := json.Marshal(struct {
		DryRun bool `json:"dryRun"`
		orthancService.TenantStudySyncReport
	}{dryRun, report})
	fmt.Println(string(out))
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfill failed:", err)
		return 1
	}
	return 0
}
