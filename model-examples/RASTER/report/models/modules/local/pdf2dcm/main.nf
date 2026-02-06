process PDF2DCM {
    tag "$meta.id"

    container "guillaumeth/raster:dev"

    input:
    tuple val(meta), path(pdf), path(dicom), path(json)

    output:
    tuple val(meta), path("*__raster.dcm"), emit: raster
    path "versions.yml"                  , emit: versions

    when:
    task.ext.when == null || task.ext.when

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    def version = workflow.manifest.version
    """
    data=\$(cat ${json})
    date=\$(date '+%Y%m%d')
    time=\$(date '+%H%M%S')
    studyDate=\$(dcmdump --search 0008,0020 ${dicom} +T | rev | cut -d" " -f1 | rev | tr -d "[" | tr -d "]")
    studyTime=\$(dcmdump --search 0008,0030 ${dicom} +T | rev | cut -d" " -f1 | rev | tr -d "[" | tr -d "]")
    AcquistionDateTime=\$(dcmdump --search 0008,002a ${dicom} +T | rev | cut -d" " -f1 | rev | tr -d "[" | tr -d "]")
    StudyID=\$(dcmdump --search 0020,0010 ${dicom} +T | rev | cut -d" " -f1 | rev | tr -d "[" | tr -d "]")

    pdf2dcm ${pdf} ${prefix}__raster.dcm --study-from ${dicom}\
        --title RASTER --key 0008,0070="Avnirlab"\
        --key 0008,103e="RASTER" --key 0008,0021="\${date}"\
        --key 0008,0030="\${time}" --key 0008,0020="\${studyDate}"\
        --key 0008,0030="\${studyTime}" --key 0008,002a="\${AcquistionDateTime}"\
        --key 0020,0010="\${StudyID}" --key 2025,0010="RASTER"\
        --key 2025,0011="${version}"

    dcmodify -nrc -i "(2025,0012)=\${data}" -nb ${prefix}__raster.dcm

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        dcmtk: \$(pdf2dcm --version | head -n 1)
    END_VERSIONS
    """

    stub:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    touch ${prefix}__raster.pdf

    cat <<-END_VERSIONS > versions.yml
    "${task.process}":
        dcmtk: \$(pdf2dcm --version | head -n 1)
    END_VERSIONS
    """
}
