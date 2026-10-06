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

- CathEF, CathEF-CLIP, and EchoPrime accepted 50 simultaneous callers without duplicate model copies, OOM failures, or leaked reservations.
- `maxConcurrentInferences=1` protects GPU memory but converts concurrency into queue latency; throughput is bounded by warm single-inference speed.
- Hardware sizing alone is insufficient for a 50-user service-level claim. Execution time, queue timeout, operation timeout, lifecycle responsiveness, and recovery behavior materially determine capacity.
- The CT failure was not an A100 memory-capacity failure: physical and declared VRAM remained within budget.
- Repeat the final benchmark from a separate load-generator host after #354 is fixed.
