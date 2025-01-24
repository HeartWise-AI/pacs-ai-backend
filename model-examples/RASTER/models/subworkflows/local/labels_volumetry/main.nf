include { VOLUMETRY_LABELS } from '../../../modules/local/volumetry_labels/main.nf'
include { VOLUMETRY_REPORT } from '../../../modules/local/volumetry_report/main.nf'
include {CT_BET} from '../../../modules/local/ct_bet/main.nf'
include {STROKE_SEGMENTATION} from '../../../modules/local/stroke_segmentation/main.nf'


workflow LABELS_VOLUMETRY {

    take:
    volumes         // path

    main:
    ch_versions = Channel.empty()
    CT_BET( volumes )
    ch_versions = ch_versions.mix(CT_BET.out.versions)

    STROKE_SEGMENTATION(volumes)
    ch_versions = ch_versions.mix(STROKE_SEGMENTATION.out.versions)

    ch_volumetry = STROKE_SEGMENTATION.out.labels
        .join(CT_BET.out.brain_mask)
    VOLUMETRY_LABELS( ch_volumetry )
    ch_versions = ch_versions.mix(VOLUMETRY_LABELS.out.versions.first())

    ch_report = STROKE_SEGMENTATION.out.labels
        .join(volumes)
        .join(VOLUMETRY_LABELS.out.volumetry_report)
    VOLUMETRY_REPORT( ch_report )
    ch_versions = ch_versions.mix(VOLUMETRY_REPORT.out.versions.first())

    emit:
    versions = ch_versions          // channel: [ versions.yml ]
}
