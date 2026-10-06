# Concurrent Inference Results

Dates: 2026-10-05 to 2026-10-06

## Environment

- NVIDIA A100-SXM4-80GB with a 77,824 MiB scheduling budget and 4,096 MiB safety margin
- Intel Xeon Processor (Icelake), 13 logical CPUs, and 235 GiB system RAM
- API revision `8c833bd`
- 600-second model queue timeout and 300-second model operation timeout
- Load generator on the PACS-AI host through loopback
- Three repetitions per completed concurrency level
- Fixed de-identified XA and Echo fixtures and the 133-slice LIDC-IDRI-0001 CT series

The benchmark measured infrastructure behavior, not clinical accuracy. Result bundles contain no prediction bodies, access tokens, DICOM paths, patient identifiers, or raw DICOM metadata.

## CathEF

| Users | Success | Req/min | Median (s) | p95 (s) | p99 (s) | Max (s) | Peak VRAM MiB | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 144.43 | 0.40 | 0.44 | 0.45 | 0.45 | 603 | 0 |
| 5 | 100% | 207.19 | 0.94 | 1.45 | 1.47 | 1.47 | 603 | 1 |
| 10 | 100% | 226.42 | 1.54 | 2.63 | 2.68 | 2.69 | 603 | 6 |
| 25 | 100% | 232.53 | 3.45 | 6.20 | 6.44 | 6.53 | 3,053 | 21 |
| 50 | 100% | 225.51 | 7.35 | 13.06 | 14.53 | 14.88 | 3,053 | 46 |

Cold-start latency was 1.44 seconds. All 275 measured requests succeeded. Throughput plateaued around 225-233 requests per minute while tail latency increased with FIFO queue depth.

## CathEF-CLIP

CathEF-CLIP was measured on 2026-10-06 with the same de-identified XA fixture and protocol.

| Users | Success | Req/min | Median (s) | p95 (s) | p99 (s) | Max (s) | Peak VRAM MiB | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 90.59 | 0.66 | 0.69 | 0.70 | 0.70 | 2,579 | 0 |
| 5 | 100% | 114.71 | 1.61 | 2.63 | 2.71 | 2.73 | 2,579 | 3 |
| 10 | 100% | 115.11 | 2.97 | 5.19 | 5.25 | 5.25 | 2,579 | 8 |
| 25 | 100% | 118.50 | 6.52 | 12.20 | 12.68 | 12.86 | 2,579 | 23 |
| 50 | 100% | 118.38 | 12.92 | 24.25 | 25.26 | 25.47 | 2,579 | 48 |

Cold-start latency was 3.13 seconds. All 275 measured requests succeeded. Throughput plateaued around 115-118 requests per minute. The stable 2,579 MiB physical GPU high-water mark and 3,320 MiB manager reservation remained within the declared resource contract.

+## Additional XA workload scope

The following XA models were measured on 2026-10-06 with one fixed de-identified XA cine per request. End-to-end latency includes Model Manager queueing, semaphore admission, readiness checks, inference, and response delivery. Peak queue is sampled once per second and can under-report instantaneous depth for very fast models. Isolated peak-VRAM contracts remain documented in the resource benchmark because physical GPU samples in this sequential multi-model run include models already resident from earlier scenarios.

## DeepRV

| Users | Success | Req/min | Median (s) | p95 (s) | Max (s) | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 78.75 | 0.78 | 0.84 | 0.85 | 0 |
| 5 | 100% | 83.51 | 2.22 | 3.67 | 3.82 | 4 |
| 10 | 100% | 85.09 | 3.99 | 6.90 | 7.35 | 8 |
| 25 | 100% | 87.74 | 8.95 | 16.51 | 17.41 | 23 |
| 50 | 100% | 89.22 | 17.37 | 32.08 | 34.04 | 48 |

Cold-start latency was 1.59 seconds. All 275 measured requests succeeded.

## DeepRV-CLIP

| Users | Success | Req/min | Median (s) | p95 (s) | Max (s) | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 76.72 | 0.78 | 0.82 | 0.82 | 0 |
| 5 | 100% | 91.68 | 2.00 | 3.27 | 3.34 | 3 |
| 10 | 100% | 95.31 | 3.54 | 6.26 | 6.37 | 8 |
| 25 | 100% | 94.86 | 8.29 | 15.15 | 15.90 | 23 |
| 50 | 100% | 94.79 | 16.22 | 30.35 | 31.80 | 48 |

Cold-start latency was 2.66 seconds. All 275 measured requests succeeded.

## DeepCORO-CTO

| Users | Success | Req/min | Median (s) | p95 (s) | Max (s) | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 58.13 | 1.04 | 1.04 | 1.05 | 0 |
| 5 | 100% | 66.65 | 2.80 | 4.51 | 4.55 | 4 |
| 10 | 100% | 67.42 | 4.97 | 8.84 | 9.02 | 9 |
| 25 | 100% | 68.63 | 11.48 | 21.02 | 21.99 | 24 |
| 50 | 100% | 69.04 | 22.26 | 41.65 | 43.57 | 49 |

Cold-start latency was 2.77 seconds. All 275 measured requests succeeded.

## DeepCORO-SYNTAX

| Users | Success | Req/min | Median (s) | p95 (s) | Max (s) | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 257.95 | 0.24 | 0.25 | 0.25 | 0 |
| 5 | 100% | 563.27 | 0.37 | 0.53 | 0.57 | 0 |
| 10 | 100% | 641.83 | 0.60 | 0.93 | 0.93 | 0 |
| 25 | 100% | 736.18 | 1.15 | 1.96 | 2.05 | 13 |
| 50 | 100% | 768.31 | 2.11 | 3.73 | 3.90 | 39 |

Cold-start latency was 3.15 seconds. All 275 measured requests succeeded.

## DeepCORO-MACE

| Users | Success | Req/min | Median (s) | p95 (s) | Max (s) | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 276.04 | 0.22 | 0.24 | 0.24 | 0 |
| 5 | 100% | 588.53 | 0.36 | 0.51 | 0.52 | 0 |
| 10 | 100% | 650.97 | 0.58 | 0.91 | 0.93 | 0 |
| 25 | 100% | 727.32 | 1.17 | 1.98 | 2.09 | 14 |
| 50 | 100% | 751.69 | 2.17 | 3.82 | 4.00 | 40 |

Cold-start latency was 2.07 seconds. All 275 measured requests succeeded.

## DeepCoro_CLIP_generic

| Users | Success | Req/min | Median (s) | p95 (s) | Max (s) | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 58.75 | 1.01 | 1.04 | 1.05 | 0 |
| 5 | 100% | 66.97 | 2.76 | 4.49 | 4.52 | 3 |
| 10 | 100% | 67.37 | 4.94 | 8.87 | 8.99 | 9 |
| 25 | 100% | 68.29 | 11.53 | 21.12 | 22.01 | 24 |
| 50 | 100% | 68.50 | 22.48 | 41.96 | 43.90 | 49 |

Cold-start latency was 3.14 seconds. All 275 measured requests succeeded.

## CardioSyntax

| Users | Success | Req/min | Median (s) | p95 (s) | Max (s) | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 57.59 | 1.06 | 1.06 | 1.06 | 0 |
| 5 | 100% | 67.19 | 2.80 | 4.50 | 4.50 | 4 |
| 10 | 100% | 68.37 | 4.89 | 8.72 | 8.89 | 9 |
| 25 | 100% | 68.01 | 11.63 | 21.19 | 22.09 | 24 |
| 50 | 100% | 68.60 | 22.38 | 41.93 | 43.81 | 49 |

Cold-start latency was 3.39 seconds. All 275 measured requests succeeded.

## DeepCoro_CLIP_ProcedureViewClassifier

Not measured because the model is not deployed.


## EchoPrime

| Users | Success | Req/min | Median (s) | p95 (s) | p99 (s) | Max (s) | Peak VRAM MiB | Peak queue |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 100% | 6.67 | 8.98 | 9.03 | 9.03 | 9.03 | 1,647 | 0 |
| 5 | 100% | 6.95 | 25.97 | 43.23 | 43.45 | 43.50 | 4,151 | 4 |
| 10 | 100% | 6.96 | 47.55 | 86.03 | 86.39 | 86.49 | 4,151 | 9 |
| 25 | 100% | 6.96 | 112.52 | 207.57 | 216.04 | 216.36 | 4,151 | 24 |
| 50 | 100% | 6.95 | 220.29 | 414.23 | 431.49 | 432.97 | 4,151 | 49 |

Cold-start latency was 12.90 seconds. All 275 measured requests succeeded. Throughput remained near 6.95 requests per minute while latency scaled with queue position under the one-inference semaphore. All 50-user requests completed within the 600-second queue timeout.

## TotalSegmentator Boundary

| Users | Repetitions | Success | HTTP status | Median of successes (s) | p95 of successes (s) | Peak queue |
| ---: | ---: | ---: | --- | ---: | ---: | ---: |
| 1 | 3 | 100% | 3 x 200 | 15.19 | 15.58 | 0 |
| 5 | 3 | 33.3% | 5 x 200; 10 x 500 | 42.66 | 67.72 | 4 |
| 10 | 1 | 0% | 10 x 500 | n/a | n/a | 9 |

Cold-start latency was 19.43 seconds. Repeated concurrent CT inference made the lifecycle endpoint unresponsive. The gateway returned bounded lifecycle context errors and one `DOCKER_INFERENCE_ERROR`; the manager entered `EVICTING` and reported failed cleanup and recovery attempts.

An earlier all-model run reached the first 50-user CT burst and observed 48 execution timeouts with 48 failed cleanup and recovery events before the safety stop. Repeating the unsafe burst was not justified. TotalSegmentator and `api-pacs` were restarted after each failed attempt, and the final state was verified as ready with zero loaded models, queue depth, GPU reservations, and physical GPU use.

The mixed 50-user scenario was not run because it would include the same unstable CT path and contaminate the valid XA and Echo measurements. The blocking defect is tracked in #354.

## Interpretation

- All nine deployed XA models and EchoPrime accepted 50 simultaneous callers without duplicate model copies, OOM failures, or leaked reservations.
- `maxConcurrentInferences=1` protects GPU memory but converts concurrency into queue latency; throughput is bounded by warm single-inference speed.
- Hardware sizing alone is insufficient for a 50-user service-level claim. Execution time, queue timeout, operation timeout, lifecycle responsiveness, and recovery behavior materially determine capacity.
- The CT failure was not an A100 memory-capacity failure: physical and declared VRAM remained within budget.
- Repeat the final benchmark from a separate load-generator host after #354 is fixed.
