# DeepRV-CLIP

DeepCORO-CLIP based binary classifier for right ventricular systolic function from coronary angiograms.

Model weights are hosted on HuggingFace at [heartwise/DeepRV_CLIP](https://huggingface.co/heartwise/DeepRV_CLIP)
and fetched at Docker build time from the immutable revision recorded in
`data/model_info.json`. The build verifies `best_model_epoch_4.pt` against its
declared SHA-256 before creating the image.

## Official release

Official images are built from reviewed `master` commits by the protected
`Publish inference model image` GitHub Actions workflow. Select
`model-examples/DeepRV-CLIP` and leave `publish_latest` disabled. The workflow
publishes the immutable semantic-version tag, records its digest, and registers
the verified artifact in the PACS-AI model catalog.

Do not build or push the official tag manually and do not overwrite the legacy
`heartwisehub/deeprv-clip:1.0` image.

## Local build

For local development, place the verified checkpoint in `models/` and build the
explicitly local image:

```
cp /path/to/best_model_epoch_4.pt models/
docker build --platform linux/amd64 -f Dockerfile.local -t deeprv-clip:local .
```

The local Dockerfile verifies the checkpoint fingerprint and marks the image
with `ai.heartwise.release.channel=local`; it is not an official release path.

## Run

```
# CPU
docker run -p 8000:8000 deeprv-clip:local

# GPU
docker run -p 8000:8000 --gpus all deeprv-clip:local

# Interactive shell on pacs-net
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash deeprv-clip:local
```

## Inputs

- `main_structure` ∈ {`Left Coronary`, `Right Coronary`}
- `status` = `diagnostic`
- Up to 6 videos per study (extras are ignored after sorting by SeriesTime).

## Outputs

- `JSON` and `HTML` output modes
- `predictions.abnormalRV.probability` — P(abnormal RV systolic function) after sigmoid
- `predictions.abnormalRV.diagnosis` — `normal` or `abnormal` using 0.5 threshold

## Report of a stored result

`POST /inference/render-report` with `{"result": <data of a JSON-mode predict response>}` returns
`{"htmlBase64", "modelVersion"}`: the same HTML document HTML-mode predict returns for that prediction.
It runs on the CPU, does not load the model, and does not count as model activity.
`GET /inference/model-info` advertises it as `capabilities.renderReport: true`.

Nothing stored is copied into the page. The stored numbers are checked (type, finite, range, decimals),
then `CustomPredictionService.report_html` looks for a raw model output whose JSON output, regenerated
by the predict code, is the stored result, and renders the report from it. A result whose values or
text are not what this model version writes for those numbers (an edited recommendation, a result of
another model version with other thresholds) is refused with 422 (`INVALID_RESULT`); a body over
64 KB is refused with 413.

## Changelog

- 1.0.3: security patch: PyTorch 2.13.0 (CUDA 12.6 build), torchvision 0.28.0, closing the open Dependabot
  PyTorch advisories (incl. critical GHSA-53q9-r3pm-6pq6 and high GHSA-63cw-57p8-fm3p). Unpinned Python
  dependencies are now pinned to the versions of the previous image. Same weights, preprocessing, thresholds
  and report; predictions checked against the previous image on synthetic and real XA studies.
- 1.0.2: includes `POST /inference/render-report` and `capabilities.renderReport` in model-info
  (no image of 1.0.2 was published before this was added). The endpoint does not change the
  model, weights, preprocessing, predictions or live report.
