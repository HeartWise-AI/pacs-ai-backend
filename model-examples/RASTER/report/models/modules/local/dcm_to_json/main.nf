process DCM_TO_JSON {
    tag "$meta.id"

    container "guillaumeth/raster:dev"

    input:
    tuple val(meta), path(dcm)

    output:
    tuple val(meta), path("*__longitudinal_volumetry.json"), emit: volumetry_report
    path "versions.yml"                       , emit: versions

    when:
    task.ext.when == null || task.ext.when

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    dcm2json ${dcm} | jq -r '."20250012".Value[0]' > ${prefix}__longitudinal_volumetry.json

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        dcmtk: ??
    END_VERSIONS
    """

    stub:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    touch ${prefix}__volumetry.json

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        dcmtk: ??
    END_VERSIONS
    """
}