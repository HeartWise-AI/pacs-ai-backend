# Angiogram Model Resource Benchmark

Date: 2026-10-02

## Environment

- GPU: NVIDIA A100-SXM4-80GB (81,920 MiB)
- GPU baseline with all measured model containers stopped: 4 MiB
- Input: local anonymized `DEMO-CARDIO-001` XA study
- GPU sampling interval: 50 ms through NVML
- Warm latency: median of three successful requests
- All measured requests returned HTTP 200 with `success: true`

## Results

| Model | Version | Active weights | Resident VRAM | Peak VRAM | Cold first inference | Warm inference |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CathEF | 1.6.1 | 57.9 MiB | 599.6 MiB (0.59 GiB) | 3,793.9 MiB (3.70 GiB) | 5.26 s | 4.21 s |
| CathEF-CLIP | 1.0.0 | 431.7 MiB | 2,575.6 MiB (2.52 GiB) | 3,319.9 MiB (3.24 GiB) | 6.11 s | 3.50 s |
| DeepRV | 1.0.0 | 18.7 MiB | 1,515.6 MiB (1.48 GiB) | 2,259.9 MiB (2.21 GiB) | 11.43 s | 10.36 s |
| DeepRV-CLIP | 1.0.0 | 434.1 MiB | 3,501.6 MiB (3.42 GiB) | 4,245.9 MiB (4.15 GiB) | 7.30 s | 5.03 s |
| DeepCORO-CTO | 2.0.0 | 155.4 MiB | 5,743.6 MiB (5.61 GiB) | 6,487.9 MiB (6.34 GiB) | 10.22 s | 8.16 s |
| DeepCORO-SYNTAX | 5.0.0 | 439.5 MiB | 5,745.6 MiB (5.61 GiB) | 6,489.9 MiB (6.34 GiB) | 10.52 s | 7.98 s |
| DeepCORO-MACE | 1.0.0 | 418.1 MiB | 2,099.6 MiB (2.05 GiB) | 2,843.9 MiB (2.78 GiB) | 5.32 s | 2.88 s |
| DeepCoro_CLIP_generic | 1.0.0 | 434.3 MiB | 5,743.6 MiB (5.61 GiB) | 6,487.9 MiB (6.34 GiB) | 12.48 s | 10.42 s |
| CardioSyntax | 1.0.0 | 400.1 MiB | 5,699.6 MiB (5.57 GiB) | 6,443.9 MiB (6.29 GiB) | 10.26 s | 7.78 s |
| DeepCoro_CLIP_ProcedureViewClassifier | 1.0.0 | 41.9 MiB | 2,215.8 MiB (2.16 GiB) | 2,215.8 MiB (2.16 GiB) | 1.96 s | 1.41 s |

## Method

Each deployed model was measured in isolation. Its container was stopped to release GPU memory, restarted,
and queried through its direct `/api/inference/predict` endpoint. The DICOM payload was built before timing.
The first request measured model loading plus inference; three subsequent requests measured warm inference.
The model was then stopped, and GPU memory was verified to return to the 4 MiB baseline before proceeding.
All model containers were restored to their original running state after the benchmark.

The active-weights column is the size of the checkpoint file or files selected by the running container's
configuration, not the Docker image size. Resident VRAM is the stabilized incremental GPU allocation after
inference. Peak VRAM is measured relative to the stopped-container GPU baseline and is the relevant raw value
for resource scheduling before applying an operational safety margin.

## Input Sizes

| Model group | XA videos per request |
| --- | ---: |
| CathEF | 5 |
| CathEF-CLIP | 4 |
| DeepRV-CLIP | 6 |
| DeepCORO-CTO | 10 |
| DeepCORO-SYNTAX | 10 |
| DeepCORO-MACE | 3 |
| DeepRV | 10 |
| DeepCoro_CLIP_generic | 10 |
| CardioSyntax | 10 |
| DeepCoro_CLIP_ProcedureViewClassifier | 1 |

The three historical model definitions expose `dicomUploadMax: 99`, but DeepCoro_CLIP_generic and
CardioSyntax are architecturally configured for ten videos. Those historical models, including DeepRV, were
therefore benchmarked with ten videos. Their `model_info.json` limits should be corrected before claiming a
99-video worst-case measurement.

The procedure-view classifier was measured separately with the largest XA DICOM in the same demo study: one
101-frame, 512 x 512 video. The published image currently reconstructs the model on every prediction because
its `load_model` implementation does not short-circuit when already initialized. Its warm latency therefore
includes that repeated initialization behavior and should be rechecked after the loader is made idempotent.

`Cold first inference` excludes Docker startup and HTTP-readiness time. It begins when the first prediction
request is sent to a freshly restarted and ready container, and includes model-weight loading, payload transfer,
preprocessing, inference, and response generation. Warm measurements include the same request path without
weight loading. Payload construction time is excluded from both.
