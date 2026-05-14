# DeepRV-CLIP

DeepCORO-CLIP based binary classifier for right ventricular systolic function from coronary angiograms.

Model weights are hosted on HuggingFace at [heartwise/DeepRV_CLIP](https://huggingface.co/heartwise/DeepRV_CLIP)
and fetched at Docker build time via `--mount=type=secret,id=hf_token`.

## Build & push

Put your HuggingFace read token in `hf_token.txt` (gitignored via `*.txt`):

```
echo "<your_hf_token>" > hf_token.txt
docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/deeprv-clip:1.0 .
docker push heartwisehub/deeprv-clip:1.0
```

## Run

```
# CPU
docker run -p 8000:8000 heartwisehub/deeprv-clip:1.0

# GPU
docker run -p 8000:8000 --gpus all heartwisehub/deeprv-clip:1.0

# Interactive shell on pacs-net
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash heartwisehub/deeprv-clip:1.0
```

## Inputs

- `main_structure` ∈ {`Left Coronary`, `Right Coronary`}
- `status` = `diagnostic`
- Up to 6 videos per study (extras are ignored after sorting by SeriesTime).

## Outputs

- `JSON` and `HTML` output modes
- `predictions.abnormalRV.probability` — P(abnormal RV systolic function) after sigmoid
- `predictions.abnormalRV.diagnosis` — `normal` or `abnormal` using 0.5 threshold
