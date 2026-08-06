package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"api-pacs/infrastructures/database/postgresql"
	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	inferenceRepository "api-pacs/module/inference/infrastructure/repository"
	inferenceService "api-pacs/module/inference/infrastructure/service"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "validate legacy processing state without writing")
	flag.Parse()
	if !*dryRun {
		fmt.Fprintln(os.Stderr, "refusing to run: --dry-run is the only supported mode")
		os.Exit(2)
	}

	database := &postgresql.PostgreSQLDBHandler{}
	if err := database.Connect(postgresqlTypes.ConnectionParams{
		DBHost:     os.Getenv("POSTGRES_DB_HOST"),
		DBPort:     os.Getenv("POSTGRES_DB_PORT"),
		DBDatabase: os.Getenv("POSTGRES_DB_DATABASE"),
		DBUsername: os.Getenv("POSTGRES_DB_USERNAME"),
		DBPassword: os.Getenv("POSTGRES_DB_PASSWORD"),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to inference database: %v\n", err)
		os.Exit(1)
	}

	repository := &inferenceRepository.InferenceProcessingRunRepository{
		PostgresSQLDBHandlerInterface: database,
	}
	service := &inferenceService.InferenceQueryService{
		InferenceProcessingRunRepositoryInterface: repository,
	}
	report, err := service.DryRunLegacyProcessingRunBackfill(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot plan legacy processing-run backfill: %v\n", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "cannot encode dry-run report: %v\n", err)
		os.Exit(1)
	}
}
