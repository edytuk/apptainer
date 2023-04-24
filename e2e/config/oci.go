// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2019-2023, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package config

import (
	"fmt"
	"testing"

	"github.com/apptainer/apptainer/e2e/internal/e2e"
)

//nolint:maintidx
func (c configTests) ociConfigGlobal(t *testing.T) {
	e2e.EnsureOCIArchive(t, c.env)
	archiveRef := "oci-archive:" + c.env.OCIArchivePath

	setDirective := func(t *testing.T, directive, value string) {
		c.env.RunApptainer(
			t,
			e2e.WithProfile(e2e.RootProfile),
			e2e.WithCommand("config global"),
			e2e.WithArgs("--set", directive, value),
			e2e.ExpectExit(0),
		)
	}
	resetDirective := func(t *testing.T, directive string) {
		c.env.RunApptainer(
			t,
			e2e.WithProfile(e2e.RootProfile),
			e2e.WithCommand("config global"),
			e2e.WithArgs("--reset", directive),
			e2e.ExpectExit(0),
		)
	}

	tests := []struct {
		name              string
		argv              []string
		profile           e2e.Profile
		addRequirementsFn func(*testing.T)
		cwd               string
		directive         string
		directiveValue    string
		exit              int
		resultOp          e2e.ApptainerCmdResultOp
	}{
		// {
		// 	name:           "AllowPidNsNo",
		// 	argv:           []string{"--pid", "--no-init", archiveRef, "/bin/sh", "-c", "echo $$"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "allow pid ns",
		// 	directiveValue: "no",
		// 	exit:           0,
		// 	resultOp:       e2e.ExpectOutput(e2e.UnwantedExactMatch, "1"),
		// },
		// {
		// 	name:           "AllowPidNsYes",
		// 	argv:           []string{"--pid", "--no-init", archiveRef, "/bin/sh", "-c", "echo $$"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "allow pid ns",
		// 	directiveValue: "yes",
		// 	exit:           0,
		// 	resultOp:       e2e.ExpectOutput(e2e.ExactMatch, "1"),
		// },
		{
			name: "ConfigPasswdNo",
			argv: []string{archiveRef, "grep",
				fmt.Sprintf("%s:x:%d", e2e.OCIUserProfile.ContainerUser(t).Name, e2e.OCIUserProfile.ContainerUser(t).UID),
				"/etc/passwd"},
			profile:        e2e.OCIUserProfile,
			directive:      "config passwd",
			directiveValue: "no",
			exit:           1,
		},
		{
			name: "ConfigPasswdYes",
			argv: []string{archiveRef, "grep",
				fmt.Sprintf("%s:x:%d", e2e.OCIUserProfile.ContainerUser(t).Name, e2e.OCIUserProfile.ContainerUser(t).UID),
				"/etc/passwd"},
			profile:        e2e.OCIUserProfile,
			directive:      "config passwd",
			directiveValue: "yes",
			exit:           0,
		},
		{
			name: "ConfigGroupNo",
			argv: []string{archiveRef, "grep",
				fmt.Sprintf("x:%d:%s", e2e.OCIUserProfile.ContainerUser(t).GID, e2e.OCIUserProfile.ContainerUser(t).Name),
				"/etc/group"},
			profile:        e2e.OCIUserProfile,
			directive:      "config group",
			directiveValue: "no",
			exit:           1,
		},
		{
			name: "ConfigGroupYes",
			argv: []string{archiveRef, "grep",
				fmt.Sprintf("x:%d:%s", e2e.OCIUserProfile.ContainerUser(t).GID, e2e.OCIUserProfile.ContainerUser(t).Name),
				"/etc/group"},
			profile:        e2e.OCIUserProfile,
			directive:      "config group",
			directiveValue: "yes",
			exit:           0,
		},
		// {
		// 	name:           "ConfigResolvConfNo",
		// 	argv:           []string{archiveRef, "grep", "/etc/resolv.conf.*- tmpfs", "/proc/self/mountinfo"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "config resolv_conf",
		// 	directiveValue: "no",
		// 	exit:           1,
		// },
		// {
		// 	name:           "ConfigResolvConfYes",
		// 	argv:           []string{archiveRef, "grep", "/etc/resolv.conf.*- tmpfs", "/proc/self/mountinfo"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "config resolv_conf",
		// 	directiveValue: "yes",
		// 	exit:           0,
		// },
		// {
		// 	name:           "MountProcNo",
		// 	argv:           []string{archiveRef, "test", "-d", "/proc/self"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount proc",
		// 	directiveValue: "no",
		// 	exit:           1,
		// },
		// {
		// 	name:           "MountProcYes",
		// 	argv:           []string{archiveRef, "test", "-d", "/proc/self"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount proc",
		// 	directiveValue: "yes",
		// 	exit:           0,
		// },
		// {
		// 	name:           "MountSysNo",
		// 	argv:           []string{archiveRef, "test", "-d", "/sys/kernel"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount sys",
		// 	directiveValue: "no",
		// 	exit:           1,
		// },
		// {
		// 	name:           "MountSysYes",
		// 	argv:           []string{archiveRef, "test", "-d", "/sys/kernel"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount sys",
		// 	directiveValue: "yes",
		// 	exit:           0,
		// },
		// {
		// 	name:           "MountDevNo",
		// 	argv:           []string{archiveRef, "test", "-d", "/dev/pts"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount dev",
		// 	directiveValue: "no",
		// 	exit:           1,
		// },
		// {
		// 	name:           "MountDevMinimal",
		// 	argv:           []string{archiveRef, "test", "-b", "/dev/loop0"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount dev",
		// 	directiveValue: "minimal",
		// 	exit:           1,
		// },
		// {
		// 	name:           "MountDevYes",
		// 	argv:           []string{archiveRef, "test", "-b", "/dev/loop0"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount dev",
		// 	directiveValue: "yes",
		// 	exit:           0,
		// },
		// // just test 'mount devpts = no' as yes depends of kernel version
		// {
		// 	name:           "MountDevPtsNo",
		// 	argv:           []string{"-C", archiveRef, "test", "-d", "/dev/pts"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount devpts",
		// 	directiveValue: "no",
		// 	exit:           1,
		// },
		// {
		// 	name:           "MountHomeNo",
		// 	argv:           []string{archiveRef, "test", "-d", u.Dir},
		// 	profile:        e2e.OCIUserProfile,
		// 	cwd:            "/",
		// 	directive:      "mount home",
		// 	directiveValue: "no",
		// 	exit:           1,
		// },
		// {
		// 	name:           "MountHomeYes",
		// 	argv:           []string{archiveRef, "test", "-d", u.Dir},
		// 	profile:        e2e.OCIUserProfile,
		// 	cwd:            "/",
		// 	directive:      "mount home",
		// 	directiveValue: "yes",
		// 	exit:           0,
		// },
		// {
		// 	name:           "MountTmpNo",
		// 	argv:           []string{archiveRef, "test", "-d", c.env.TestDir},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount tmp",
		// 	directiveValue: "no",
		// 	exit:           1,
		// },
		// {
		// 	name:           "MountTmpYes",
		// 	argv:           []string{archiveRef, "test", "-d", c.env.TestDir},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "mount tmp",
		// 	directiveValue: "yes",
		// 	exit:           0,
		// },
		// {
		// 	name:           "BindPathPasswd",
		// 	argv:           []string{archiveRef, "test", "-f", "/passwd"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "bind path",
		// 	directiveValue: "/etc/passwd:/passwd",
		// 	exit:           0,
		// },
		// {
		// 	name:           "UserBindControlNo",
		// 	argv:           []string{"--bind", "/etc/passwd:/passwd", archiveRef, "test", "-f", "/passwd"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "user bind control",
		// 	directiveValue: "no",
		// 	exit:           1,
		// },
		// {
		// 	name:           "UserBindControlYes",
		// 	argv:           []string{"--bind", "/etc/passwd:/passwd", archiveRef, "test", "-f", "/passwd"},
		// 	profile:        e2e.OCIUserProfile,
		// 	directive:      "user bind control",
		// 	directiveValue: "yes",
		// 	exit:           0,
		// },
	}

	for _, tt := range tests {
		c.env.RunApptainer(
			t,
			e2e.AsSubtest(tt.name),
			e2e.WithProfile(tt.profile),
			e2e.WithDir(tt.cwd),
			e2e.PreRun(func(t *testing.T) {
				if tt.addRequirementsFn != nil {
					tt.addRequirementsFn(t)
				}
				setDirective(t, tt.directive, tt.directiveValue)
			}),
			e2e.PostRun(func(t *testing.T) {
				resetDirective(t, tt.directive)
			}),
			e2e.WithCommand("exec"),
			e2e.WithArgs(tt.argv...),
			e2e.ExpectExit(tt.exit, tt.resultOp),
		)
	}
}
