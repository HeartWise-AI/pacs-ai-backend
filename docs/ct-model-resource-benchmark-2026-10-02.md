# CT Model Resource Benchmark

Date: 2026-10-02

## Environment

- GPU: NVIDIA A100-SXM4-80GB (81,920 MiB)
- GPU baseline with the measured model container stopped: 4 MiB
- GPU sampling interval: 50 ms through NVML
- Timing: one cold request followed by one warm request for each successfully measured model
- Request payload: complete DICOM instances grouped as one series through `seriesInstanceImages`
- Pixel data was not duplicated in `seriesInstanceMetadata`

## Results

| Model | Version | Active weights | Resident VRAM | Peak VRAM | Cold first inference | Warm inference |
| --- | ---: | --- | ---: | ---: | ---: | ---: |
| BrainGPT (Otter-Image) | 1.0.0 | 30.29 GiB in four local shards | 32,795.8 MiB (32.03 GiB) | 32,795.8 MiB (32.03 GiB) | 304.20 s | 190.19 s |
| Hemorrhage | 1.0.0 | 332.7 MiB (fold 0) | 1,439.9 MiB (1.41 GiB) | 4,631.9 MiB (4.52 GiB) | 223.46 s | 221.13 s |
| TotalSegmentator | 2.9.0 | 157.3 MiB (`total`, fast 3 mm model) | 1,253.9 MiB (1.22 GiB) | 4,245.9 MiB (4.15 GiB) | 17.35 s | 13.24 s |

All three measured requests returned HTTP 200 with `success: true`. BrainGPT required a temporary
network-enabled benchmark container because its published image does not package the tokenizer referenced as
`EleutherAI/gpt-neox-20b`. The resource measurement is valid, but the deployed offline container remains unable
to load until those tokenizer files are bundled into the image.

## Inputs

### Cranial input: BrainGPT and Hemorrhage

- Source series: `HN-CHUM-065`, one 263-slice axial CT series
- Selected subvolume: the 48 most superior contiguous slices, DICOM instances 1 through 48
- Matrix: 512 x 512 pixels
- DICOM position range: -160.69 mm to -7.00 mm, a 153.7 mm physical span
- Compressed DICOM size: 24.2 MiB
- Request payload: 32.3 MiB

The full 263-slice series spans close to one metre of head-and-neck anatomy and is not representative of the
Hemorrhage model's cerebral target. A discarded exploratory run on the full series produced 2,057 nnU-Net
sliding windows and an estimated inference time of roughly 18 minutes per request. No result from that
interrupted exploratory run is included in this report.

BrainGPT internally samples 24 slices from the received volume. Hemorrhage reconstructs the selected 48-slice
series as a 3D NIfTI volume and runs nnU-Net inference. BrainGPT was rerun successfully against the anatomically
ordered 48-slice superior subvolume after the initial generic benchmark selection was found to sort files by
compressed size rather than anatomy.

### Thoracic input: TotalSegmentator

- Source series: `LIDC-IDRI-0001`, one complete 133-slice chest CT series
- Matrix: 512 x 512 pixels
- Slice thickness: 2.5 mm
- In-plane pixel spacing: 0.703125 x 0.703125 mm
- Compressed DICOM size: 66.8 MiB
- Request payload: 89.0 MiB

TotalSegmentator ran the deployed `task="total"`, `fast=True`, `device="gpu"` path.

## Method

Each model was tested in isolation. Its container was stopped to release GPU memory, restarted, and queried
through its direct `/inference/predict` endpoint. The first request measured model loading plus the complete
request path. One subsequent request measured warm inference. The container was then stopped, GPU memory was
verified to return to the 4 MiB baseline, and the container was restored to its original running state.

`Cold first inference` excludes Docker startup and HTTP-readiness time. It includes model loading, payload
transfer, DICOM-to-volume processing, inference, response encoding, and response transfer. Payload construction
time is excluded. Unlike the angiogram and Echo benchmarks, the CT warm value is one successful measurement,
not the median of three requests, because the 3D Hemorrhage inference takes approximately 3.7 minutes per run.

Resident and peak VRAM are reported relative to the stopped-container 4 MiB baseline. Before first inference,
the ready CT APIs held approximately 748.3 MiB total VRAM due to their CUDA runtime context.

## Model-specific observations

### BrainGPT

The image contains four local model shards totaling 30.29 GiB. The original deployment benchmark returned HTTP
500 before loading those shards because initialization requires the `EleutherAI/gpt-neox-20b` tokenizer, which
is not bundled in the image and cannot be fetched by an offline PACS AI container.

For resource measurement only, a temporary container was allowed to retrieve the missing tokenizer files and
then ran the same local checkpoint against the 48-slice cranial input. It stabilized at 32.03 GiB above the
stopped-container baseline; no additional peak beyond that resident allocation was observed at 50 ms sampling.
The published image must still package the tokenizer and be retested in the normal offline deployment path
before BrainGPT can be considered operationally deployable.

### Hemorrhage

Five fold checkpoints are present in the image, but the deployed code sets `use_folds = 0,`, which is the tuple
`(0,)`. Only the 332.7 MiB fold 0 checkpoint is active in this measurement.

### TotalSegmentator

The deployed fast `total` task uses a single 157.3 MiB checkpoint. The measured value does not represent the
higher-resolution non-fast configuration or other TotalSegmentator tasks.

## Interpretation

- Largest measured CT resident allocation: 32.03 GiB for BrainGPT.
- Largest measured CT peak allocation: 32.03 GiB for BrainGPT.
- All three deployed CT APIs declare `maxConcurrentInferences: 1` and serialize the complete load/predict path.
- The raw peak is rounded upward into `peakMemoryMiB`; model admission still needs an operational safety
  margin and must be specific to the supported input envelope.
- Volume dimensions, physical spacing, inference task, and fast/full mode are material parts of a CT model's
  resource declaration. Slice count alone is insufficient.

## Next validation

- Bundle BrainGPT's tokenizer dependencies for offline deployment and verify the self-contained image reproduces
  the successful temporary-container measurement.
- Validate Hemorrhage on a dedicated representative non-contrast head CT series.
- Measure CT VRAM and latency across supported volume sizes and voxel spacings.
- Decide whether TotalSegmentator `fast=True` is the production contract and benchmark full-resolution mode if not.
- Define and validate the operational VRAM safety margin.
- Run concurrent-user latency tests through the production request path.
