#!/bin/bash

# Set the URL of the Hugging Face repository
repo_url="huggingface.co/heartwise/PACS-AI.git"

echo clone https://"${HF_USERNAME}":"${HF_TOKEN}"@"${repo_url}"

# Clone the repository using Git LFS
GIT_LFS_SKIP_SMUDGE=1 git clone https://"${HF_USERNAME}":"${HF_TOKEN}"@"${repo_url}" repo

# Check if the clone was successful
if [ $? -eq 0 ]; then
    echo "Repository cloned successfully."

    # Navigate to the repository directory
    cd repo || exit

    # Pull the large file using Git LFS
    git lfs pull

    # Check if the Git LFS pull was successful
    if [ $? -eq 0 ]; then
        echo "Git LFS pull successful."


        # Create the torch_serving_model directory if it doesn't exist
        mkdir -p ../torch_serving_model

         # Extract the latest version number from the JSON file
        latest_version=$(jq -r '."fr"."Changelogs" | keys[]' model_info/cath-ef.json | sort -V | tail -n1)

        # Navigate to the CATH_EF directory
        cd CATH_EF/X3D_1 || exit

        # Run torch-model-archiver on X3D_1 directory
        # Replace the following command with the actual command for X3D_1
        torch-model-archiver --model-name X3D_1 --version "$latest_version" --model-file X3D_1.py --serialized-file best.pt --handler handler_1.py --requirements-file model_requirements.txt --extra-files modelParts.py --export-path ../../../torch_serving_model/ -f

        # Check if the archiving was successful
        if [ $? -eq 0 ]; then
            echo "Archiving X3D_1 successful."
        else
            echo "Archiving X3D_1 failed."
        fi

        cd ../X3D_2 || exit

        # Run torch-model-archiver on X3D_2 directory
        # Replace the following command with the actual command for X3D_2
        torch-model-archiver --model-name X3D_2 --version "$latest_version" --model-file X3D_2.py --serialized-file best.pt --handler handler_2.py --requirements-file model_requirements.txt --extra-files modelParts.py --export-path ../../../torch_serving_model/ -f

        # Check if the archiving was successful
        if [ $? -eq 0 ]; then
            echo "Archiving X3D_2 successful."
        else
            echo "Archiving X3D_2 failed."
        fi

        cd ..

        mv config.properties ../../torch_serving_model/config.properties

        # Navigate out of the CATH_EF directory
        cd ..

        if [ -d "../model_info" ]; then rm -Rf "../model_info"; fi

        mv model_info ../model_info

        cd ..

        # Remove the repository directory
        rm -rf repo

        echo "Repository directory removed."
    else
        echo "Git LFS pull failed."
    fi
else
    echo "Repository cloning failed."
fi