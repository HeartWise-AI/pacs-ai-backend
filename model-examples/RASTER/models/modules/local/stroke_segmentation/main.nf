process STROKE_SEGMENTATION {
    tag "$meta.id"

    container "guillaumeth/mbh-seg_2i-mtl:evaluate_latest"

    input:
    tuple val(meta), path(volume)

    output:
    tuple val(meta), path("*__labels.nii.gz"), emit: labels
    path "versions.yml"                      , emit: versions

    when:
    task.ext.when == null || task.ext.when

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    mkdir dataset pred
    mv "${volume}" dataset
    python /src/mean_teacher/evaluate.py --data_dir dataset --save_dir pred --chkpt_file /weigths.ckpt --config_file /hparams.yaml
    mv pred/*.nii.gz ${prefix}__labels.nii.gz
    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        mbh-seg_2i-mtl: 0.1.0
    END_VERSIONS
    """

    stub:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    touch ${prefix}__labels.nii.gz

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        mbh-seg_2i-mtl: 0.1.0
    END_VERSIONS
    """
}
