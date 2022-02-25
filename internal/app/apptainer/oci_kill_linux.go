// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2018-2019, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package apptainer

import (
	"os"
	"os/exec"

	"github.com/apptainer/apptainer/pkg/sylog"
)

// OciKill kills container process
func OciKill(containerID string, killSignal string) error {
	runcArgs := []string{
		"--root", RuncStateDir,
		"kill",
		containerID,
		killSignal,
	}

	cmd := exec.Command(runc, runcArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdout
	sylog.Debugf("Calling runc with args %v", runcArgs)
	return cmd.Run()
}
