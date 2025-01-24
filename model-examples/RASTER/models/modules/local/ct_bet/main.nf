process CT_BET {
    tag "$meta.id"

    container "avnirlab/ctbet:latest"

    input:
    tuple val(meta), path(volume)

    output:
    tuple val(meta), path("*__brain_mask.nii.gz"), emit: brain_mask
    path "versions.yml"                      , emit: versions

    when:
    task.ext.when == null || task.ext.when

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    nnUNetv2_install_pretrained_model_from_zip /assets/nnUNetv2_pretrained_model.zip
    volume="${volume}"
    if [ "\${volume##*_}" != "0000.nii.gz" ]; then
        mv "${volume}" "\${volume%.nii.gz}_0000.nii.gz"
    fi
    mkdir input output
    mv *_0000.nii.gz input

    nnUNetv2_predict -i input -o output -d 001 -c 3d_fullres -f all -device ${params.device}

    mv output/*.nii.gz ${prefix}__brain_mask.nii.gz
    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        nnunetv2: \$(pip list | grep nnunetv2 | tr -s ' ' | cut -d " " -f 2)
    END_VERSIONS
    """

    stub:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    touch ${prefix}__brain_mask.nii.gz

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        nnunetv2: \$(pip list | grep nnunetv2 | tr -s ' ' | cut -d " " -f 2)
    END_VERSIONS
    """
}
