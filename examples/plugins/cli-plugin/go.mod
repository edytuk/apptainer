module github.com/apptainer/apptainer/cli-example-plugin

go 1.16

require (
	github.com/apptainer/apptainer v0.0.0
	github.com/spf13/cobra v1.2.1
)

replace github.com/apptainer/apptainer => ./singularity_source
