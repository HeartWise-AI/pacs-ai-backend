
# RASTER - RAdiology STrokE Report
![GitHub Release](https://img.shields.io/github/v/release/llgneuroresearch/raster)
[![Documentation Status](https://readthedocs.org/projects/avnir-documentation/badge/?version=latest)](https://avnir-documentation.readthedocs.io/en/latest/tools/pipelines.html)

RASTER is a radiology stroke report generation pipeline that processes CT brain scans to automatically generate structured reports for stroke analysis. The pipeline uses advanced image processing and AI techniques to detect and analyze stroke-related abnormalities.

## Installation

Install Nextflow by following the instructions on the [Nextflow website](https://www.nextflow.io/).
```sh
# Install Nextflow
curl -s https://get.nextflow.io | bash
mv nextflow /usr/local/bin/

# Verify installation
nextflow -v
```

## Usage

To run the pipeline, use the following example:

```sh
nextflow pull llgneuroresearch/raster -r main
nextflow run llgneuroresearch/raster -r main --input input -with-profile docker
```

### Description

- `--input=/path/to/[root]`: Root folder containing multiple subjects
    ```
    [root]
    ├── S1
    │   └── *ct.nii.gz
    └── S2
        └── *ct.nii.gz
    ```

### Optional Arguments

- `--output_dir`: Directory where to write the final results. By default, will be in "./results".

### Available Profiles

- `docker`: Use Docker containers.
- `apptainer`: Use Apptainer containers.
- `singularity`: Use Singularity containers.
- `slurm`: Use Slurm executor.