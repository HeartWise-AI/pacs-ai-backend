include { PDF2DCM } from '../../../modules/local/pdf2dcm/main.nf'

workflow CONVERT_TO_DCM {

    take:
    pdf         // path
    data       // path
    dicom      // path

    main:
    ch_versions = Channel.empty()
    ch_pdf = pdf.join(dicom).join(data)

    PDF2DCM(ch_pdf)
    ch_versions = ch_versions.mix(PDF2DCM.out.versions)

    emit:
    versions = ch_versions          // channel: [ versions.yml ]
}
