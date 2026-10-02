# Radiography Model Resource Benchmark

Date: 2026-10-02

## Environment

- GPU: NVIDIA A100-SXM4-80GB (81,920 MiB)
- GPU baseline with each measured model container stopped: 4 MiB
- Input: official pydicom `RG1_UNCI.dcm` CR test image
- Input dimensions: 1,955 x 1,841 pixels, 16 bits allocated, 15 bits stored
- GPU sampling interval: 50 ms through NVML
- Warm latency: median of three successful requests
- All measured requests returned HTTP 200 with `success: true`

## Results

| Model | Version | Active weights | Resident VRAM | Peak VRAM | Cold first inference | Warm inference |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CIED-AI | 1.0.0 | 660 MiB packaged TensorFlow models | 4,391.8 MiB (4.29 GiB) | 4,391.8 MiB (4.29 GiB) | 9.92 s | 0.80 s |
| MedGemma 1.5 4B | 1.5.0 | 8.01 GiB in two safetensor shards | 9,711.8 MiB (9.48 GiB) | 9,711.8 MiB (9.48 GiB) | 16.76 s | 10.29 s |

## Method

The published images were downloaded and measured one at a time in temporary GPU-enabled containers. The first
request measured model loading plus inference, and three subsequent requests measured warm inference. Each
temporary container was removed after measurement, and GPU memory was verified to return to the 4 MiB baseline
before proceeding.

CIED-AI received the pixel metadata required by its API in addition to the complete DICOM. MedGemma received the
complete DICOM without duplicated pixel metadata. Resident and peak VRAM are reported relative to the stopped-
container baseline, matching the angiogram, Echo, and CT benchmark convention.

## Model-specific observations

### CIED-AI

The published image packages three TensorFlow SavedModels for device detection, image quality, and device type.
Its TensorFlow server is configured with `per_process_gpu_memory_fraction=0.1`; consequently, the stabilized
allocation dominated the observed peak. The public CR test image is suitable for exercising the image pipeline
but is not a validated positive CIED case. A positive-device radiograph should be tested before treating the
latency result as a clinical worst case.

### MedGemma

The image packages `google/medgemma-1.5-4b-it` locally and loads it in bfloat16 with automatic device placement.
The request used the model's default prompt, `Describe this medical image`, and its production generation path.
The observed peak was identical to the stabilized resident allocation at the 50 ms sampling interval.

## Interpretation

- Largest measured radiography resident allocation: 9.48 GiB for MedGemma.
- Largest measured radiography peak allocation: 9.48 GiB for MedGemma.
- Both APIs declare `maxConcurrentInferences: 1` and serialize the complete load/predict path.
- The measurements cover one 1,955 x 1,841 CR image and one request at a time.
- The raw peaks are inputs to model admission; the production reservation still requires explicit operational
  headroom and validation against the supported input envelope.

## Next Validation

- Repeat CIED-AI using a representative positive-device chest radiograph.
- Validate MedGemma latency and output-length sensitivity on representative chest radiographs.
- Define and validate the operational VRAM safety margin.
- Run concurrent-user latency tests through the production managed-inference path.
