// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package actions

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/apptainer/apptainer/e2e/internal/e2e"
)

const (
	dockerArchiveURI = "https://github.com/apptainer/apptainer/releases/download/v0.1.0/alpine-docker-save.tar"
)

func (c actionTests) actionOciRun(t *testing.T) {
	e2e.EnsureOCIImage(t, c.env)

	// Prepare docker-archive source
	tmpDir := t.TempDir()
	dockerArchive := filepath.Join(tmpDir, "docker-archive.tar")
	err := e2e.DownloadFile(dockerArchiveURI, dockerArchive)
	if err != nil {
		t.Fatalf("Could not download docker archive test file: %v", err)
	}
	defer os.Remove(dockerArchive)
	// Prepare oci source (oci directory layout)
	ociLayout := t.TempDir()
	cmd := exec.Command("tar", "-C", ociLayout, "-xf", c.env.OCIImagePath)
	err = cmd.Run()
	if err != nil {
		t.Fatalf("Error extracting oci archive to layout: %v", err)
	}

	tests := []struct {
		name     string
		imageRef string
		argv     []string
		exit     int
	}{
		{
			name:     "docker-archive",
			imageRef: "docker-archive:" + dockerArchive,
			exit:     0,
		},
		{
			name:     "oci-archive",
			imageRef: "oci-archive:" + c.env.OCIImagePath,
			exit:     0,
		},
		{
			name:     "oci",
			imageRef: "oci:" + ociLayout,
			exit:     0,
		},
		{
			name:     "true",
			imageRef: "oci:" + ociLayout,
			argv:     []string{"true"},
			exit:     0,
		},
		{
			name:     "false",
			imageRef: "oci:" + ociLayout,
			argv:     []string{"false"},
			exit:     1,
		},
	}

	for _, profile := range []e2e.Profile{e2e.OCIRootProfile, e2e.OCIUserProfile} {
		t.Run(profile.String(), func(t *testing.T) {
			for _, tt := range tests {
				cmdArgs := []string{tt.imageRef}
				cmdArgs = append(cmdArgs, tt.argv...)
				c.env.RunApptainer(
					t,
					e2e.AsSubtest(tt.name),
					e2e.WithProfile(e2e.OCIRootProfile),
					e2e.WithCommand("run"),
					// While we don't support args we are entering a /bin/sh interactively.
					e2e.ConsoleRun(e2e.ConsoleSendLine("exit")),
					e2e.WithArgs(cmdArgs...),
					e2e.ExpectExit(tt.exit),
				)
			}
		})
	}
}

// exec tests min fuctionality for apptainer exec
func (c actionTests) actionOciExec(t *testing.T) {
	e2e.EnsureOCIImage(t, c.env)

	imageRef := "oci-archive:" + c.env.OCIImagePath

	tests := []struct {
		name string
		argv []string
		exit int
	}{
		{
			name: "NoCommand",
			argv: []string{imageRef},
			exit: 1,
		},
		{
			name: "True",
			argv: []string{imageRef, "true"},
			exit: 0,
		},
		{
			name: "TrueAbsPAth",
			argv: []string{imageRef, "/bin/true"},
			exit: 0,
		},
		{
			name: "False",
			argv: []string{imageRef, "false"},
			exit: 1,
		},
		{
			name: "FalseAbsPath",
			argv: []string{imageRef, "/bin/false"},
			exit: 1,
		},
	}

	for _, tt := range tests {
		c.env.RunApptainer(
			t,
			e2e.AsSubtest(tt.name),
			e2e.WithProfile(e2e.UserProfile),
			e2e.WithCommand("exec"),
			e2e.WithDir("/tmp"),
			e2e.WithArgs(tt.argv...),
			e2e.ExpectExit(tt.exit),
		)
	}
}

// Shell interaction tests
func (c actionTests) actionOciShell(t *testing.T) {
	e2e.EnsureOCIImage(t, c.env)

	tests := []struct {
		name       string
		argv       []string
		consoleOps []e2e.ApptainerConsoleOp
		exit       int
	}{
		{
			name: "ShellExit",
			argv: []string{"oci-archive:" + c.env.OCIImagePath},
			consoleOps: []e2e.ApptainerConsoleOp{
				// "cd /" to work around issue where a long
				// working directory name causes the test
				// to fail because the "Apptainer" that
				// we are looking for is chopped from the
				// front.
				// TODO(mem): This test was added back in 491a71716013654acb2276e4b37c2e015d2dfe09
				e2e.ConsoleSendLine("cd /"),
				e2e.ConsoleExpect("Apptainer"),
				e2e.ConsoleSendLine("exit"),
			},
			exit: 0,
		},
		{
			name: "ShellBadCommand",
			argv: []string{"oci-archive:" + c.env.OCIImagePath},
			consoleOps: []e2e.ApptainerConsoleOp{
				e2e.ConsoleSendLine("_a_fake_command"),
				e2e.ConsoleSendLine("exit"),
			},
			exit: 127,
		},
	}

	for _, tt := range tests {
		c.env.RunApptainer(
			t,
			e2e.AsSubtest(tt.name),
			e2e.WithProfile(e2e.OCIUserProfile),
			e2e.WithCommand("shell"),
			e2e.WithArgs(tt.argv...),
			e2e.ConsoleRun(tt.consoleOps...),
			e2e.ExpectExit(tt.exit),
		)
	}
}
