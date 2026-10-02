# Echo Model Resource Benchmark

Date: 2026-10-02

## Environment

- GPU: NVIDIA A100-SXM4-80GB (81,920 MiB)
- GPU baseline with the measured model container stopped: 4 MiB
- Input: one local anonymized echocardiography study containing five multiframe US DICOM clips
- Input dimensions: 600 x 800 pixels, 251 frames total
- Compressed input size: 25.9 MiB across the five DICOM files
- Request payload size: 34.5 MiB
- GPU sampling interval: 50 ms through NVML
- Warm latency: median of three successful requests
- All measured requests returned HTTP 200 with `success: true`

## Results

| Model | Version | Active GPU-loaded artifacts | Resident VRAM | Peak VRAM | Cold first inference | Warm inference |
| --- | ---: | --- | ---: | ---: | ---: | ---: |
| EchoPrime | 1.2.1 | 466.4 MiB checkpoints + 450.5 MiB candidate embeddings | 2,265.9 MiB (2.21 GiB) | 5,153.9 MiB (5.03 GiB) | 15.12 s | 8.50 s |
| PanEcho | 1.0.0 | 481.7 MiB checkpoint; configured view-classifier checkpoint absent | 1,441.9 MiB (1.41 GiB) | 2,869.9 MiB (2.80 GiB) | 9.46 s | 6.53 s |
| EchoPrime View Classifier | 1.0.0 | 132.3 MiB checkpoint | 1,965.8 MiB (1.92 GiB) | 1,965.8 MiB (1.92 GiB) | 14.93 s | 13.12 s |

## Method

Each deployed model was measured in isolation. Its container was stopped to release GPU memory, restarted,
and queried through its direct `/inference/predict` endpoint. The first request measured model loading plus
inference; three subsequent requests measured warm inference. The model was then stopped, and GPU memory was
verified to return to the 4 MiB baseline before proceeding. The two previously deployed Echo model containers
were restored to their original running state after the benchmark, and the temporary view-classifier container
was removed.

The request sent the five complete compressed DICOM files through `seriesInstanceImages`. Pixel data was not
duplicated in `seriesInstanceMetadata`. EchoPrime was running with `USE_FULL_DICOM=true`; PanEcho consumes the
same full-DICOM representation directly.

`Cold first inference` excludes Docker startup and HTTP-readiness time. It begins when the first prediction
request is sent to a freshly restarted and ready container, and includes model-weight loading, payload transfer,
preprocessing, inference, and response generation. Warm measurements include the same request path without
weight loading. Payload construction time is excluded from both.

Resident and peak VRAM are reported relative to the stopped-container 4 MiB GPU baseline, matching the
angiogram benchmark convention. After the API became ready but before its first inference, each Echo container
held a 748.3 MiB total CUDA context allocation, or 744.3 MiB above the stopped-container baseline.

## Model-specific observations

### EchoPrime

The deployed service loads two model checkpoints and a candidate-report embedding tensor onto the GPU:

- Echo encoder: 132.2 MiB
- View classifier: 334.2 MiB
- Candidate report embeddings: 450.5 MiB
- Total GPU-loaded model artifacts: 917.0 MiB

Other report-retrieval assets are loaded into system memory and are not included in the weights column.

### PanEcho

The deployed image contains the 481.7 MiB PanEcho checkpoint. Its configuration also references
`/app/weights/best_model_pretrained_echoprime_updated.pth`, but that view-classifier checkpoint is absent from
the running container. The service therefore logged that it was falling back to unfiltered inference and used
all five DICOM clips successfully.

The reported PanEcho VRAM values accurately describe the current deployed image, but they should be remeasured
after the view-classifier checkpoint is added because both resident and peak VRAM may increase.

### EchoPrime View Classifier

The standalone view-classifier image was measured against the same five complete US DICOM clips. It processes
each qualifying clip sequentially using 16 sampled frames resized to 224 x 224. The peak observed allocation
was identical to the stabilized resident allocation at the 50 ms sampling interval.

## Interpretation

- Largest measured Echo resident allocation: 2.21 GiB for EchoPrime.
- Largest measured Echo peak allocation: 5.03 GiB for EchoPrime.
- All three measured Echo APIs declare `maxConcurrentInferences: 1` and serialize the complete load/predict path.
- The raw peak is rounded upward into `peakMemoryMiB`; production admission still needs
  an explicit operational safety margin.
- These measurements cover a five-clip study and one request at a time. They do not establish concurrent-user
  capacity or the worst case allowed by the declared `dicomUploadMax: 99` limit.

## Next validation

- Measure latency and VRAM scaling across representative Echo study sizes.
- Run concurrent-user latency tests through the production request path.
- Define and validate the operational VRAM safety margin.
- Add or correct the PanEcho view-classifier checkpoint, then repeat its benchmark.
- Reconcile the declared `dicomUploadMax: 99` limit with clinically representative and supported study sizes.
