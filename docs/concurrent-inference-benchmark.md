# Concurrent inference benchmark

`scripts/benchmark_concurrent_inference.py` measures PACS-AI behavior under synchronized inference demand. It targets the managed gateway, not individual prediction endpoints, so the results include Model Manager queueing, GPU admission, model loading, inference, and eviction.

The benchmark characterizes infrastructure behavior. It does not assess clinical accuracy.

## Workload

The publication protocol uses CathEF, EchoPrime, and TotalSegmentator as representative XA, echocardiography, and CT workloads. For each model, the runner:

1. Confirms every runtime is already unloaded through its lifecycle endpoint and that the manager has no resident reservation.
2. Records one cold request.
3. Sends one warm-up request.
4. Runs synchronized warm bursts at 1, 5, 10, 25, and 50 concurrent requests.
5. Repeats each level three times.

An optional mixed-model burst distributes 50 requests across the three models. The example uses a balanced 17/17/16 distribution. Replace it with a clinically informed distribution when trustworthy utilization data is available, and record that choice in `notes`.

Same-model requests are expected to serialize because V1 declares `maxConcurrentInferences=1`. Increasing latency is therefore expected. The benchmark checks that requests queue predictably without duplicate model copies, OOM failures, or leaked reservations.

## Configuration

Copy `scripts/concurrent-inference.example.json` outside the repository's tracked files and update:

- `gatewayUrl`: authenticated `api-pacs` managed inference endpoint.
- `metricsUrl`: authenticated `api-pacs/debug/vars` endpoint.
- `tenantId`: tenant authorized to use every configured model.
- `deploymentName` and `loadGeneratorLocation`: exact environment and network placement.
- `serverConfiguration`: tested PACS-AI GPU, CPU, system RAM, manager budget, and safety margin.
- `dicomPaths`: fixed, de-identified fixture files or directories.
- `lifecycleUrl`: model lifecycle API reachable from the load generator.

`requireColdStart=true` requires every model to define `lifecycleUrl`. The preflight is read-only: it verifies that every configured runtime is `UNLOADED` and that the manager reports zero loaded models and zero resident reservation. It never unloads behind the manager's back.

Before starting the publication run, unload all benchmark models through their lifecycle endpoints, wait for their supervised processes to return, and restart `api-pacs`. The restart lets startup reconciliation rebuild an empty reservation ledger. All measured predictions then go through the managed gateway.

Set `localResourceSampling=true` only when the runner executes on the PACS-AI GPU host. It samples `/proc` and `nvidia-smi`. For a separate load-generator host, set it to `false` and collect physical server telemetry on the PACS-AI host in parallel. Manager telemetry is always collected remotely when `metricsUrl` is configured.

## Installation

```bash
python -m venv .venv-benchmark
.venv-benchmark/bin/pip install -r scripts/requirements_concurrent_inference.txt
```

Provide secrets through environment variables. They are read at runtime and are never written to result files:

```bash
export STUDY_SERVICE_CALLBACK_TOKEN='...'
export OPENAPI_DOCS_PASSWORD='...'
```

Use a controlled maintenance window. A same-model 50-user CT burst can remain queued for several minutes because inference is intentionally serialized.

## Dry run

Before the publication run, use a temporary configuration with concurrency levels `[1, 2]`, one repetition, and the mixed scenario disabled. Then run:

```bash
python scripts/benchmark_concurrent_inference.py \
  --config /path/to/dry-run.json \
  --output-dir benchmark-results/dry-run
```

The command exits nonzero if any summarized request fails, if the manager is not ready before the run, or if the manager finishes with a nonempty queue or active GPU reservation.

## Publication run

Run the committed protocol without changing concurrency or repetition counts:

```bash
python scripts/benchmark_concurrent_inference.py \
  --config /path/to/publication-run.json
```

The default timestamped directory contains:

- `run-config.json`: sanitized configuration and host metadata.
- `requests.csv`: one row per request with timing and bounded outcome fields.
- `samples.csv`: timestamped host and Model Manager gauges.
- `summary.json`: grouped results, per-run metric deltas, and final manager state.
- `report.md`: methodology, limitations, and a reviewer-ready results table.

The result bundle never contains access tokens, prediction bodies, DICOM paths, or extracted patient identifiers. DICOM payloads remain in memory.

## Interpretation

Report median, p95, p99, maximum latency, throughput, success rate, peak queue depth, and peak GPU/system resource use for every concurrency level. Keep cold-start measurements separate from warm bursts. State whether the load generator ran locally, inside `pacs-net`, or from a separate host because network placement changes the meaning of end-to-end latency.

Use metric deltas from each run rather than cumulative process counters. A valid completed run ends with Model Manager readiness equal to one, queue depth zero, and active reservation zero. Resident reservation may remain nonzero because warm models intentionally stay loaded until capacity requires eviction.
