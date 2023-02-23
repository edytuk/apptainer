// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2019-2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

// Package bin provides access to external binaries
package bin

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/apptainer/apptainer/internal/pkg/buildcfg"
	"github.com/apptainer/apptainer/pkg/sylog"
	"github.com/apptainer/apptainer/pkg/util/apptainerconf"
	"github.com/pkg/errors"
)

// FindBin returns the path to the named binary, or an error if it is not found.
// We don't list any default because we want a deliberate decision about whether
// to use the SuidBinaryPath which is more restrictive when in the suid flow.
func FindBin(name string) (path string, err error) {
	switch name {
	// Basic system executables that we assume are always on PATH
	// We will search for these only in default PATH when in the suid flow
	case "cp",
		"dd",
		"mkfs.ext3",
		"mknod",
		"mount",
		"nsenter",
		"rm",
		"stdbuf",
		"true",
		"truncate",
		"uname":
		return findOnPath(name, true)
	// Executables that might be run privileged from the suid flow
	// We must not search the user's PATH when in the suid flow with these
	case "cryptsetup":
		return findOnPath(name, true)
	// All other executables
	// We will always search the user's PATH first for these
	case "conmon",
		"crun",
		"curl",
		"debootstrap",
		"dnf",
		"fakeroot",
		"fakeroot-sysv",
		"fuse-overlayfs",
		"fuse2fs",
		"go",
		"ldconfig",
		"mksquashfs",
		"newgidmap",
		"newuidmap",
		"nvidia-container-cli",
		"pacstrap",
		"rpm",
		"rpmkeys",
		"runc",
		"squashfuse",
		"squashfuse_ll",
		"SUSEConnect",
		"unsquashfs",
		"yum",
		"zypper":
		return findOnPath(name, false)
	}
	return "", fmt.Errorf("unknown executable name %q", name)
}

// findOnPath performs a search on the configurated binary path for the
// named executable, returning its full path.
func findOnPath(name string, useSuidPath bool) (path string, err error) {
	cfg := apptainerconf.GetCurrentConfig()
	if cfg == nil {
		if strings.HasSuffix(os.Args[0], ".test") {
			// read config if doing unit tests
			cfg, err = apptainerconf.Parse(buildcfg.APPTAINER_CONF_FILE)
			if err != nil {
				return "", errors.Wrap(err, "unable to parse apptainer configuration file")
			}
			apptainerconf.SetCurrentConfig(cfg)
		} else {
			sylog.Fatalf("configuration not pre-loaded in findOnPath")
		}
	}
	if cfg.SuidBinaryPath == "" {
		if strings.HasSuffix(os.Args[0], ".test") {
			apptainerconf.SetBinaryPath(buildcfg.LIBEXECDIR, true)
		} else {
			sylog.Fatalf("SetBinaryPath has not been run before findOnPath")
		}
	}
	var newPath string
	if useSuidPath {
		sylog.Debugf("Searching for %q in SuidBinaryPath", name)
		newPath = cfg.SuidBinaryPath
	} else {
		newPath = cfg.BinaryPath
	}
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", newPath)

	path, err = exec.LookPath(name)
	if err == nil {
		sylog.Debugf("Found %q at %q", name, path)
	}
	return path, err
}
