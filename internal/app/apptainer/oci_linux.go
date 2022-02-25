// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2018-2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

// Includes code from https://github.com/containers/podman
// Released under the Apache License Version 2.0

package apptainer

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/apptainer/apptainer/internal/pkg/util/fs"
	"github.com/apptainer/apptainer/internal/pkg/util/user"
	"github.com/apptainer/apptainer/pkg/syfs"
	"github.com/apptainer/apptainer/pkg/util/fs/lock"
	securejoin "github.com/cyphar/filepath-securejoin"
)

const (
	// Absolute path for runc
	runc = "/usr/bin/runc"
	// Absolute path for conmon
	conmon = "/usr/bin/conmon"
	// Absolute path for the runc state
	RuncStateDir = "/run/apptainer-oci"
	// Relative path inside ~/.apptainer for conmon and apptainer state
	ociPath = "oci"
	// State directory files
	containerPidFile = "container.pid"
	containerLogFile = "container.log"
	runcLogFile      = "runc.log"
	conmonPidFile    = "conmon.pid"
	bundleLink       = "bundle"
	// Files in the OCI bundle root
	bundleLock   = ".apptainer-oci.lock"
	attachSocket = "attach"
	// Timeouts
	createTimeout = 30 * time.Second
)

// OciArgs contains CLI arguments
type OciArgs struct {
	BundlePath   string
	LogPath      string
	LogFormat    string
	PidFile      string
	FromFile     string
	KillSignal   string
	KillTimeout  uint32
	EmptyProcess bool
	ForceKill    bool
}

// AttachStreams contains streams that will be attached to the container
type AttachStreams struct {
	// OutputStream will be attached to container's STDOUT
	OutputStream io.WriteCloser
	// ErrorStream will be attached to container's STDERR
	ErrorStream io.WriteCloser
	// InputStream will be attached to container's STDIN
	InputStream *bufio.Reader
	// AttachOutput is whether to attach to STDOUT
	// If false, stdout will not be attached
	AttachOutput bool
	// AttachError is whether to attach to STDERR
	// If false, stdout will not be attached
	AttachError bool
	// AttachInput is whether to attach to STDIN
	// If false, stdout will not be attached
	AttachInput bool
}

/* Sync with stdpipe_t in conmon.c */
const (
	AttachPipeStdin  = 1
	AttachPipeStdout = 2
	AttachPipeStderr = 3
)

type ociError struct {
	Level string `json:"level,omitempty"`
	Time  string `json:"time,omitempty"`
	Msg   string `json:"msg,omitempty"`
}

// stateDir returns the path to container state handled by conmon/apptainer
// (as opposed to runc's state in RuncStateDir)
func stateDir(containerID string) (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}

	u, err := user.CurrentOriginal()
	if err != nil {
		return "", err
	}

	configDir, err := syfs.ConfigDirForUsername(u.Name)
	if err != nil {
		return "", err
	}

	rootPath := filepath.Join(configDir, ociPath)
	containerPath := filepath.Join(hostname, containerID)
	path, err := securejoin.SecureJoin(rootPath, containerPath)
	if err != nil {
		return "", err
	}
	return path, err
}

// lockBundle creates a lock file in a bundle directory
func lockBundle(bundlePath string) error {
	bl := path.Join(bundlePath, bundleLock)
	_, err := os.Stat(bl)
	if err == nil {
		return fmt.Errorf("bundle is locked by another process")
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("while stat-ing lock file: %w", err)
	}

	fd, err := lock.Exclusive(bundlePath)
	if err != nil {
		return fmt.Errorf("while acquiring directory lock: %w", err)
	}
	defer lock.Release(fd)

	err = fs.EnsureFileWithPermission(bl, 0o600)
	if err != nil {
		return fmt.Errorf("while creating lock file: %w", err)
	}
	return nil
}

// releaseBundle removes a lock file in a bundle directory
func releaseBundle(bundlePath string) error {
	bl := path.Join(bundlePath, bundleLock)
	_, err := os.Stat(bl)
	if os.IsNotExist(err) {
		return fmt.Errorf("bundle is not locked")
	}
	if err != nil {
		return fmt.Errorf("while stat-ing lock file: %w", err)
	}

	fd, err := lock.Exclusive(bundlePath)
	if err != nil {
		return fmt.Errorf("while acquiring directory lock: %w", err)
	}
	defer lock.Release(fd)

	err = os.Remove(bl)
	if err != nil {
		return fmt.Errorf("while removing lock file: %w", err)
	}
	return nil
}
