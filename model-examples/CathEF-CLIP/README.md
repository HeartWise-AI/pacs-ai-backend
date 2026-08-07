# CathEF-CLIP

DeepCORO-CLIP based LVEF estimator + binary reduced-EF classifier from left coronary angiograms.

Model weights are hosted on HuggingFace at [heartwise/CathEF_CLIP](https://huggingface.co/heartwise/CathEF_CLIP)
and fetched at Docker build time via `--mount=type=secret,id=hf_token`.

## Build & push

Put your HuggingFace read token in `hf_token.txt` (gitignored via `*.txt`):

```
echo "<your_hf_token>" > hf_token.txt
docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/cathef-clip:1.0 .
docker push heartwisehub/cathef-clip:1.0
```

## Run

```
# CPU
docker run -p 8000:8000 heartwisehub/cathef-clip:1.0

# GPU
docker run -p 8000:8000 --gpus all heartwisehub/cathef-clip:1.0

# Interactive shell on pacs-net
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash heartwisehub/cathef-clip:1.0
```

## Inputs

- `main_structure` = `Left Coronary`
- `status` = `diagnostic`
- Up to 4 videos per study (extras are ignored after sorting by SeriesTime).

## Outputs

- `JSON` and `HTML` output modes
- `predictions.LVEF.value` — LVEF % (regression, clamped to [0, 100])
- `predictions.reducedEF.probability` — P(LVEF < 40%) after sigmoid
