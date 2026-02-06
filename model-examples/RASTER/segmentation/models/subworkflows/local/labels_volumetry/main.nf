include { VOLUMETRY_LABELS } from '../../../modules/local/volumetry_labels/main.nf'
include { VOLUMETRY_REPORT } from '../../../modules/local/volumetry_report/main.nf'
include {CT_BET} from '../../../modules/local/ct_bet/main.nf'
include {STROKE_SEGMENTATION} from '../../../modules/local/stroke_segmentation/main.nf'
include { DCM2BIDS } from '../../../modules/local/dcm2bids/main.nf'
include { DCM_TO_JSON } from '../../../modules/local/dcm_to_json/main.nf'


workflow LABELS_VOLUMETRY {

    take:
    volumes         // path
    dicom           // path
    config       // path
    raster_dicom // path

    main:
    ch_versions = Channel.empty()

    DCM2BIDS( dicom, config )
    ch_versions = ch_versions.mix(DCM2BIDS.out.versions)

    DCM_TO_JSON( raster_dicom )
    ch_versions = ch_versions.mix(DCM_TO_JSON.out.versions)

    ch_volumes = DCM2BIDS.out.ct.mix(volumes)
    CT_BET( ch_volumes )
    ch_versions = ch_versions.mix(CT_BET.out.versions)

    STROKE_SEGMENTATION(ch_volumes)
    ch_versions = ch_versions.mix(STROKE_SEGMENTATION.out.versions)

    ch_volumetry = STROKE_SEGMENTATION.out.labels
        .join(CT_BET.out.brain_mask)
    VOLUMETRY_LABELS( ch_volumetry )
    ch_versions = ch_versions.mix(VOLUMETRY_LABELS.out.versions.first())

    ch_report = STROKE_SEGMENTATION.out.labels
        .join(ch_volumes)
        .join(VOLUMETRY_LABELS.out.volumetry_report)
        .join(DCM_TO_JSON.out.volumetry_report, remainder: true)
        .map{ it -> if (it[-1] == null) { [it[0], it[1], it[2], it[3], []] } else { it } }
    VOLUMETRY_REPORT( ch_report )
    ch_versions = ch_versions.mix(VOLUMETRY_REPORT.out.versions.first())

    emit:
    versions = ch_versions          // channel: [ versions.yml ]
    pdf_report = VOLUMETRY_REPORT.out.volumetry_report
    json_report = VOLUMETRY_REPORT.out.volumetry_json
}
