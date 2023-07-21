// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

// Package launcher is responsible for implementing launchers, which can start a
// container, with configuration passed from the CLI layer.
//
// The package currently implements a single native.Launcher, with an Exec
// method that constructs a runtime configuration and calls the Apptainer
// runtime starter binary to start the container.
//
// TODO - the launcher package will be extended to support launching containers
// via the OCI runc/crun runtime, in addition to the current Apptainer runtime
// starter, by adding an oci.Launcher.
package launcher

import "context"

// Launcher is responsible for configuring and launching a container image.
// It will execute a runtime, such as Apptainer's native runtime (via the starter
// binary), or an external OCI runtime (e.g. runc).
type Launcher interface {
	// Exec will execute the container image 'image', passing arguments 'args'
	// the container#s initial process. If instanceName is specified, the
	// container must be launched as a background instance, otherwist it must
	// run interactively, attached to the console.
	Exec(ctx context.Context, image string, cmd string, args []string, instanceName string) error
}
