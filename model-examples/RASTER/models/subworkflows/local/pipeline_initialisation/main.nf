
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
    outdir          // path

    main:
    ct_channel = Channel.fromPath("$input/**/*ct.nii.gz")
                    .map{ch1 ->
                        def fmeta = [:]
                        // Set meta.id
                        fmeta.id = ch1.parent.name
                        [fmeta, ch1]
                        }

    log.info "\033[0;33m Parameters \033[0m"
    log.info " Input: ${input}"
    log.info " Output directory: ${outdir}"

    emit:
    ct = ct_channel        // channel: [ val(meta), [ image ] ]
}
