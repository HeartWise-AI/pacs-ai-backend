FROM guillaumeth/mbh-seg_2i-mtl:evaluate_latest AS mbh_seg
FROM mrtrix3/mrtrix3:3.0.4 AS mrtrix3
FROM pytorch/pytorch:2.5.1-cuda12.4-cudnn9-devel

ENV MODEL_URL=https://drive.google.com/file/d/1QxbcjhYa1MlcnIar0WJMtnJNNktSjsYA/view?usp=drive_link
ENV PYTHONPATH="/:${PYTHONPATH}"
RUN ln -fs /usr/share/zoneinfo/America/New_York /etc/localtime

# Install BET
RUN DEBIAN_FRONTEND=noninteractive apt update && apt install -y wget git jq weasyprint
RUN pip install --upgrade pip
RUN pip install nnunetv2==2.4.1 gdown
RUN pip install --upgrade git+https://github.com/FabianIsensee/hiddenlayer.git
RUN pip install blosc2 acvl_utils==0.2
RUN pip install monai==1.3.0 pytorch-lightning==2.3.3 transformers==4.44.2 nibabel==5.2.1 simpleitk==2.3.1 tensorboard==2.17.1 einops==0.8.0 optuna==3.6.1
RUN mkdir /assets

# Download pretrained model
RUN gdown --fuzzy ${MODEL_URL} -O /assets/nnUNetv2_pretrained_model.zip
RUN wget https://raw.githubusercontent.com/llgneuroresearch/CTbet-Docker/refs/heads/main/assets/inference.sh -O /assets/inference.sh

# Prepare folders for NNUNETV2
RUN mkdir /input /output
WORKDIR /output
ENV nnUNet_results=/assets
ENV nnUNet_raw=/assets
ENV nnUNet_preprocessed=/assets

# Install MBH_SEG
COPY --from=mbh_seg /src /src
COPY --from=mbh_seg /weigths.ckpt /weigths.ckpt
COPY --from=mbh_seg /hparams.yaml /hparams.yaml

# Install MRTRIX3
COPY --from=mrtrix3 /opt/mrtrix3/bin/mrconvert /usr/local/bin

# Install Nextflow
RUN apt-get install -y openjdk-17-jdk-headless
RUN wget -qO- https://get.nextflow.io | bash
RUN mv nextflow /usr/local/bin/
RUN chmod +x /usr/local/bin/nextflow
RUN apt install -y rsync

# Install Avnirpy
WORKDIR /
RUN echo "Build Avnirpy"
RUN git clone https://github.com/llgneuroresearch/avnirpy
WORKDIR /avnirpy
RUN pip install -e .

# Install dcmtk and dcm2bids
RUN apt-get install -y dcmtk
RUN pip install dcm2bids
RUN apt install -y dcm2niix

# Install RASTER
WORKDIR /
COPY . /raster