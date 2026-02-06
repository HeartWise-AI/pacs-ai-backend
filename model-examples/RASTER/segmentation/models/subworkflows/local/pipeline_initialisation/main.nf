
def logoHeader(){
    // Log colors ANSI codes
    c_reset = "\033[0m";
    c_dim = "\033[2m";
    c_blue = "\033[0;34m";

    return """
    ${c_dim}-----------------------------------${c_reset}
    ${c_blue}      ___     ___   _ ___ ____  
    ${c_blue}     / \\ \\   / / \\ | |_ _|  _ \\    ${c_reset}
    ${c_blue}    / _ \\ \\ / /|  \\| || || |_) |     ${c_reset}
    ${c_blue}   / ___ \\ V / | |\\  || ||  _ <         ${c_reset}
    ${c_blue}  /_/   \\_\\_/  |_| \\_|___|_| \\_\\   ${c_reset}

    ${c_dim}------------------------------------${c_reset}
    """.stripIndent()
}

log.info logoHeader()

log.info "\033[0;33m ${workflow.manifest.name} \033[0m"
log.info "  ${workflow.manifest.description}"
log.info "  Version: ${workflow.manifest.version}"
log.info "  Github: ${workflow.manifest.homePage}"
log.info " "

workflow.onComplete {
    log.info " "
    log.info "Pipeline completed at: $workflow.complete"
    log.info "Execution status: ${ workflow.success ? 'OK' : 'failed' }"
    log.info "Execution duration: $workflow.duration"
}

workflow PIPELINE_INITIALISATION {

    take:
    input           // path
    dicom           // path
    dcm_config     // path
    raster_dicom // path
    outdir          // path

    main:
    if (input) {
        ct_channel = Channel.fromPath("$input/**/*ct.nii.gz")
                        .map{ch1 ->
                            def fmeta = [:]
                            // Set meta.id
                            fmeta.id = ch1.parent.name.replaceAll(/[^a-zA-Z0-9]/, '')
                            [fmeta, ch1]
                            }
        dicom_channel = Channel.empty()
        ch_sid_dicom = ct_channel.map{[it[0]]}
        dicom_example = Channel.empty()
    }
    else if (dicom) {
        dicom_channel = Channel.fromPath("$dicom", type:"dir")
                    .map{ch1 ->
                        def fmeta = [:]
                        // Set meta.id
                        fmeta.id = ch1.name.replaceAll(/[^a-zA-Z0-9]/, '')
                        [fmeta, ch1]
                        }
        ct_channel = Channel.empty()
        ch_sid_dicom = dicom_channel.map{[it[0]]}
        dicom_example = Channel.fromPath("$dicom/**/*[!.nii.gz,!DICOMDIR]")
        .first()
        .concat(ch_sid_dicom)
        .collect()
        .map{it -> [it[1], it[0]]}
    }

    if (raster_dicom) {
        raster_channel = Channel.fromPath("$raster_dicom/**/*[!.nii.gz,!DICOMDIR]")
                            .first()
                            .concat(ch_sid_dicom)
                            .collect()
                            .map{it -> [it[1], it[0]]}
    } else {
        raster_channel = Channel.empty()
    }

    ch_config = Channel.fromPath("$dcm_config")

    log.info "\033[0;33m Parameters \033[0m"
    log.info " Input: ${input}"
    log.info " DICOM: ${dicom}"
    log.info " Raster DICOM: ${raster_dicom}"
    log.info " Config: ${dcm_config}"
    log.info " Output directory: ${outdir}"

    emit:
    ct = ct_channel        // channel: [ val(meta), [ image ] ]
    dicom = dicom_channel  // channel: [ val(meta), [ dicoms ] ]
    dicom_example = dicom_example // channel: [ val(meta), [ dicom ] ]
    ch_config = ch_config // channel: [ config ]
    raster_dicom = raster_channel // channel: [ val(meta), [ dicoms ] ]
}
