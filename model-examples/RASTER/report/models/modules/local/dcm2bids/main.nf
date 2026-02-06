process DCM2BIDS {
    tag "$meta.id"

    container "guillaumeth/raster:dev"

    input:
    tuple val(meta), path(dicom)
    path(config)

    output:
    tuple val(meta), path("*__ct.nii.gz"), emit: ct
    path "versions.yml"                  , emit: versions

    when:
    task.ext.when == null || task.ext.when

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    prefix = prefix.replaceAll(/[^a-zA-Z0-9]/, '')
    """
    cat ${config}
    dcm2bids -d ${dicom} -p ${prefix} -c ${config}
    cp sub-${prefix}/ct/sub-${prefix}_ct.nii.gz ${prefix}__ct.nii.gz

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        dcm2bids: \$(pip list | grep dcm2bids | tr -s ' ' | cut -d " " -f 2)
    END_VERSIONS
    """

    stub:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    touch ${prefix}__ct.nii.gz

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        dcm2bids: \$(pip list | grep dcm2bids | tr -s ' ' | cut -d " " -f 2)
    END_VERSIONS
    """
}
