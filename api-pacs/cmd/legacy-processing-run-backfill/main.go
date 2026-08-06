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
	inferenceServiceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "validate legacy processing state without writing")
	apply := flag.Bool("apply", false, "apply the freshly validated legacy import plan")
	verify := flag.Bool("verify", false, "verify the persisted legacy import without writing")
	confirmation := flag.String("confirm", "", "required literal confirmation token for apply mode")
	expectedStudies := flag.Int("expected-studies", 0, "eligible study count from the immediately preceding dry run")
	expectedExecutions := flag.Int("expected-executions", 0, "eligible execution count from the immediately preceding dry run")
	flag.Parse()
	selectedModes := 0
	for _, selected := range []bool{*dryRun, *apply, *verify} {
		if selected {
			selectedModes++
		}
	}
	if selectedModes != 1 {
		fmt.Fprintln(os.Stderr, "refusing to run: select exactly one of --dry-run, --apply, or --verify")
		os.Exit(2)
	}
	if *dryRun && (*confirmation != "" || *expectedStudies != 0 || *expectedExecutions != 0) {
		fmt.Fprintln(os.Stderr, "refusing to run: apply confirmation flags cannot be used with --dry-run")
		os.Exit(2)
	}
	if *apply && (*confirmation != inferenceServiceTypes.LegacyBackfillConfirmation || *expectedStudies <= 0 || *expectedExecutions <= 0) {
		fmt.Fprintln(os.Stderr, "refusing to apply: require --confirm=LEGACY_IMPORT and positive --expected-studies/--expected-executions")
		os.Exit(2)
	}
	if *verify && (*confirmation != "" || *expectedStudies <= 0 || *expectedExecutions <= 0) {
		fmt.Fprintln(os.Stderr, "refusing to verify: require positive --expected-studies/--expected-executions and no --confirm token")
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
	var report any
	if *dryRun {
		service := &inferenceService.InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}
		var err error
		report, err = service.DryRunLegacyProcessingRunBackfill(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot plan legacy processing-run backfill: %v\n", err)
			os.Exit(1)
		}
	} else if *apply {
		service := &inferenceService.InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}
		var err error
		report, err = service.ApplyLegacyProcessingRunBackfill(context.Background(), inferenceServiceTypes.ApplyLegacyProcessingRunBackfill{
			Confirmation:       *confirmation,
			ExpectedStudies:    *expectedStudies,
			ExpectedExecutions: *expectedExecutions,
		})
		if err != nil {
			_ = writeJSON(os.Stdout, report)
			fmt.Fprintf(os.Stderr, "legacy processing-run backfill stopped: %v\n", err)
			os.Exit(1)
		}
	} else {
		service := &inferenceService.InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}
		verification, err := service.VerifyLegacyProcessingRunBackfill(context.Background(), inferenceServiceTypes.VerifyLegacyProcessingRunBackfill{
			ExpectedStudies: *expectedStudies, ExpectedExecutions: *expectedExecutions,
		})
		report = verification
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot verify legacy processing-run backfill: %v\n", err)
			os.Exit(1)
		}
		if !verification.Passed {
			_ = writeJSON(os.Stdout, report)
			fmt.Fprintln(os.Stderr, "legacy processing-run backfill verification failed")
			os.Exit(1)
		}
	}

	if err := writeJSON(os.Stdout, report); err != nil {
		fmt.Fprintf(os.Stderr, "cannot encode backfill report: %v\n", err)
		os.Exit(1)
	}
}

func writeJSON(output *os.File, report any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
