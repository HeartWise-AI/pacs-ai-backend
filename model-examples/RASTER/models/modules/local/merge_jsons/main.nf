process MERGE_JSONS {
    tag "merge_jsons"

    container "avnirlab/avnirpy:latest"

    input:
    path(jsons)

    output:
    path("all_subjects_volumetry.csv")    , emit: volumetry_csv
    path("all_subjects_volumetry.json")   , emit: volumetry_json
    path "versions.yml"         , emit: versions

    when:
    task.ext.when == null || task.ext.when

    script:
    """
    jq -s 'add' ${jsons} > all_subjects_volumetry.json
    avnir_json_to_csv all_subjects_volumetry.json all_subjects_volumetry.csv
    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        avnirpy: \$(avnir_json_to_csv --version)
    END_VERSIONS
    """

    stub:
    """
    avnir_json_to_csv -h
    touch all_subjects_volumetry.csv
    touch all_subjects_volumetry.json

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        avnirpy: \$(avnir_json_to_csv --version)
    END_VERSIONS
    """
}
