process VOLUMETRY_REPORT {
    tag "$meta.id"

    container "avnirlab/avnirpy:latest"

    input:
    tuple val(meta), path(labels), path(ct), path(json)

    output:
    tuple val(meta), path("*__volumetry_report.pdf"), emit: volumetry_report
    path "versions.yml"                       , emit: versions

    when:
    task.ext.when == null || task.ext.when

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    def patient_name = task.ext.patient_name ?: "N/A"
    def patient_id = task.ext.patient_id ?: "N/A"
    """
    avnir_create_stroke_report ${labels} ${ct} ${json} ${prefix}__volumetry_report.pdf\
        --patient_name "${patient_name}" --patient_id "${patient_id}"

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        avnirpy: \$(avnir_create_stroke_report --version)
    END_VERSIONS
    """

    stub:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    avnir_create_stroke_report -h
    touch ${prefix}__volumetry_report.pdf

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        avnirpy: \$(avnir_create_stroke_report --version)
    END_VERSIONS
    """
}