#!/usr/bin/env nextflow

nextflow.enable.dsl = 2

include { PIPELINE_INITIALISATION } from './subworkflows/local/pipeline_initialisation/main.nf'
include { LABELS_VOLUMETRY } from './subworkflows/local/labels_volumetry/main.nf'

if(params.help) {
    usage = file("$baseDir/USAGE")

    cpu_count = Runtime.runtime.availableProcessors()
    bindings = ["output_dir":"$params.output_dir"]

    engine = new groovy.text.SimpleTemplateEngine()
    template = engine.createTemplate(usage.text).make(bindings)

    print template.toString()
    return
}

/*
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    RUN MAIN WORKFLOW
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
*/

workflow {

    main:
    //
    // SUBWORKFLOW: Run initialisation tasks
    //
    PIPELINE_INITIALISATION (
        params.input,
        params.output_dir,
    )

    //
    // WORKFLOW: Run main workflow
    //
    LABELS_VOLUMETRY (
        PIPELINE_INITIALISATION.out.ct,
    )
}
