process VOLUMETRY_REPORT {
    tag "$meta.id"

    container "guillaumeth/raster:dev"

    input:
    tuple val(meta), path(labels), path(ct), path(json), path(longitudinal_json)

    output:
    tuple val(meta), path("*__volumetry_report.pdf"), emit: volumetry_report
    tuple val(meta), path("*__volumetry_report.json"), emit: volumetry_json
    path "versions.yml"                       , emit: versions

    when:
    task.ext.when == null || task.ext.when

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    def patient_name = task.ext.patient_name ?: "N/A"
    def patient_id = task.ext.patient_id ?: "N/A"
    def longitudinal_option = longitudinal_json ? "--previous_timepoint ${longitudinal_json}" : ""
    """
    avnir_create_stroke_report ${labels} ${ct} ${json} \$(date +"%Y%m%d%H%M%S")\
        ${prefix}__volumetry_report.pdf --patient_name "${patient_name}"\
        --patient_id "${patient_id}" --output_longitudinal ${prefix}__volumetry_report.json\
        ${longitudinal_option}

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