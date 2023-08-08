// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2019-2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

// Package push tests only test the oras transport (and a invalid transport) against a local registry
package push

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apptainer/apptainer/e2e/internal/e2e"
	"github.com/apptainer/apptainer/e2e/internal/testhelper"
	"github.com/pkg/errors"
)

type ctx struct {
	env e2e.TestEnv
}

func (c ctx) testInvalidTransport(t *testing.T) {
	e2e.EnsureImage(t, c.env)

	tests := []struct {
		name       string
		uri        string
		expectOp   e2e.ApptainerCmdResultOp
		expectExit int
	}{
		{
			name:       "push invalid transport",
			uri:        "nothing://bar/foo/foobar:latest",
			expectOp:   e2e.ExpectError(e2e.ContainMatch, "Unsupported transport type: nothing"),
			expectExit: 255,
		},
	}

	for _, tt := range tests {
		args := []string{c.env.ImagePath, tt.uri}

		c.env.RunApptainer(
			t,
			e2e.AsSubtest(tt.name),
			e2e.WithProfile(e2e.UserProfile),
			e2e.WithCommand("push"),
			e2e.WithArgs(args...),
			e2e.ExpectExit(tt.expectExit, tt.expectOp),
		)
	}
}

func (c ctx) testPushCmd(t *testing.T) {
	e2e.EnsureImage(t, c.env)
	e2e.EnsureOCISIF(t, c.env)

	// setup file and dir to use as invalid sources
	invalidDir, err := os.MkdirTemp(c.env.TestDir, "push_dir-")
	if err != nil {
		err = errors.Wrap(err, "creating temporary directory")
		t.Fatalf("unable to create src dir for push tests: %+v", err)
	}

	invalidFile, err := e2e.WriteTempFile(invalidDir, "invalid_image-", "Invalid Image Contents")
	if err != nil {
		err = errors.Wrap(err, "creating temporary file")
		t.Fatalf("unable to create src file for push tests: %+v", err)
	}

	tests := []struct {
		desc             string // case description
		dstURI           string // destination URI for image
		imagePath        string // src image path
		expectedExitCode int    // expected exit code for the test
		noHTTPS          bool   // --no-https/--nohttps flag
	}{
		{
			desc:             "oras non existent image",
			imagePath:        filepath.Join(invalidDir, "not_an_existing_file.sif"),
			dstURI:           fmt.Sprintf("oras://%s/oras_non_existent:test", c.env.TestRegistry),
			expectedExitCode: 255,
		},
		{
			desc:             "oras non SIF file",
			imagePath:        invalidFile,
			dstURI:           fmt.Sprintf("oras://%s/oras_non_sif:test", c.env.TestRegistry),
			expectedExitCode: 255,
		},
		{
			desc:             "oras directory",
			imagePath:        invalidDir,
			dstURI:           fmt.Sprintf("oras://%s/oras_directory:test", c.env.TestRegistry),
			expectedExitCode: 255,
		},
		{
			desc:             "oras standard SIF push",
			imagePath:        c.env.ImagePath,
			dstURI:           fmt.Sprintf("oras://%s/oras_standard_sif:test", c.env.TestRegistry),
			expectedExitCode: 0,
		},
		{
			desc:             "oras OCI-SIF push",
			imagePath:        c.env.OCISIFPath,
			dstURI:           fmt.Sprintf("oras://%s/oras_oci-sif:test", c.env.TestRegistry),
			expectedExitCode: 0,
		},
		{
			desc:             "docker non existent image",
			imagePath:        filepath.Join(invalidDir, "not_an_existing_file.sif"),
			dstURI:           fmt.Sprintf("docker://%s/docker_non_existent:test", c.env.TestRegistry),
			expectedExitCode: 255,
		},
		{
			desc:             "docker non SIF file",
			imagePath:        invalidFile,
			dstURI:           fmt.Sprintf("docker://%s/docker_non_sif:test", c.env.TestRegistry),
			expectedExitCode: 255,
		},
		{
			desc:             "docker directory",
			imagePath:        invalidDir,
			dstURI:           fmt.Sprintf("docker://%s/docker_directory:test", c.env.TestRegistry),
			expectedExitCode: 255,
		},
		{
			desc:             "docker standard SIF push",
			imagePath:        c.env.ImagePath,
			dstURI:           fmt.Sprintf("docker://%s/docker_standard_sif:test", c.env.TestRegistry),
			expectedExitCode: 255,
		},
		{
			desc:             "docker OCI-SIF push",
			imagePath:        c.env.OCISIFPath,
			dstURI:           fmt.Sprintf("docker://%s/docker_oci-sif:test", c.env.TestRegistry),
			expectedExitCode: 0,
		},
	}

	for _, tt := range tests {
		tmpdir, err := os.MkdirTemp(c.env.TestDir, "pull_test.")
		if err != nil {
			t.Fatalf("Failed to create temporary directory for pull test: %+v", err)
		}
		defer os.RemoveAll(tmpdir)

		// We create the list of arguments using a string instead of a slice of
		// strings because using slices of strings most of the type ends up adding
		// an empty elements to the list when passing it to the command, which
		// will create a failure.
		args := tt.dstURI
		if tt.imagePath != "" {
			args = tt.imagePath + " " + args
		}

		if tt.noHTTPS {
			args = "--no-https" + " " + args
		}

		c.env.RunApptainer(
			t,
			e2e.AsSubtest(tt.desc),
			e2e.WithProfile(e2e.UserProfile),
			e2e.WithCommand("push"),
			e2e.WithArgs(strings.Split(args, " ")...),
			e2e.ExpectExit(tt.expectedExitCode),
		)
	}
}

// E2ETests is the main func to trigger the test suite
func E2ETests(env e2e.TestEnv) testhelper.Tests {
	c := ctx{
		env: env,
	}

	return testhelper.Tests{
		"invalid transport": c.testInvalidTransport,
		"oras":              c.testPushCmd,
	}
}
