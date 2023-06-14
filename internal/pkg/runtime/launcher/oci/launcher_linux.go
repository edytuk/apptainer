// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

// Package oci implements a Launcher that will configure and launch a container
// with an OCI runtime. It also provides implementations of OCI state
// transitions that can be called directly, Create/Start/Kill etc.
package oci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/apptainer/apptainer/internal/pkg/buildcfg"
	"github.com/apptainer/apptainer/internal/pkg/cache"
	"github.com/apptainer/apptainer/internal/pkg/runtime/launcher"
	"github.com/apptainer/apptainer/internal/pkg/util/fs/files"
	"github.com/apptainer/apptainer/internal/pkg/util/user"
	"github.com/apptainer/apptainer/pkg/ocibundle/native"
	"github.com/apptainer/apptainer/pkg/ocibundle/tools"
	"github.com/apptainer/apptainer/pkg/syfs"
	"github.com/apptainer/apptainer/pkg/sylog"
	"github.com/apptainer/apptainer/pkg/util/apptainerconf"
	useragent "github.com/apptainer/apptainer/pkg/util/user-agent"
	"github.com/containers/image/v5/types"
	"github.com/google/uuid"
	"github.com/opencontainers/runtime-spec/specs-go"
)

var (
	ErrUnsupportedOption = errors.New("not supported by OCI launcher")
	ErrNotImplemented    = errors.New("not implemented by OCI launcher")
)

// Launcher will holds configuration for, and will launch a container using an
// OCI runtime.
type Launcher struct {
	cfg           launcher.Options
	apptainerConf *apptainerconf.File
}

// NewLauncher returns a oci.Launcher with an initial configuration set by opts.
func NewLauncher(opts ...launcher.Option) (*Launcher, error) {
	lo := launcher.Options{}
	for _, opt := range opts {
		if err := opt(&lo); err != nil {
			return nil, fmt.Errorf("%w", err)
		}
	}

	if err := checkOpts(lo); err != nil {
		return nil, err
	}

	c := apptainerconf.GetCurrentConfig()
	if c == nil {
		return nil, fmt.Errorf("apptainer configuration is not initialized")
	}

	return &Launcher{cfg: lo, apptainerConf: c}, nil
}

// checkOpts ensures that options set are supported by the oci.Launcher.
//
// nolint:maintidx
func checkOpts(lo launcher.Options) error {
	badOpt := []string{}

	if lo.Writable {
		badOpt = append(badOpt, "Writable")
	}
	if lo.WritableTmpfs {
		badOpt = append(badOpt, "WritableTmpfs")
	}
	if len(lo.OverlayPaths) > 0 {
		badOpt = append(badOpt, "OverlayPaths")
	}
	if len(lo.ScratchDirs) > 0 {
		badOpt = append(badOpt, "ScratchDirs")
	}
	if lo.WorkDir != "" {
		badOpt = append(badOpt, "WorkDir")
	}

	// Home is always sent from the CLI, and must be valid as an option, but
	// CustomHome signifies if it was a user specified value which we don't
	// support (yet).
	if lo.CustomHome {
		badOpt = append(badOpt, "CustomHome")
	}
	if lo.NoHome {
		badOpt = append(badOpt, "NoHome")
	}

	if len(lo.FuseMount) > 0 {
		badOpt = append(badOpt, "FuseMount")
	}

	if len(lo.NoMount) > 0 {
		badOpt = append(badOpt, "NoMount")
	}

	if lo.NvCCLI {
		badOpt = append(badOpt, "NvCCLI")
	}

	if len(lo.ContainLibs) > 0 {
		badOpt = append(badOpt, "ContainLibs")
	}

	if lo.CleanEnv {
		badOpt = append(badOpt, "CleanEnv")
	}
	if lo.NoEval {
		badOpt = append(badOpt, "NoEval")
	}

	// Network always set in CLI layer even if network namespace not requested.
	// We only support isolation at present
	if lo.Namespaces.Net && lo.Network != "none" {
		badOpt = append(badOpt, "Network (except none)")
	}

	if len(lo.NetworkArgs) > 0 {
		badOpt = append(badOpt, "NetworkArgs")
	}
	if lo.Hostname != "" {
		badOpt = append(badOpt, "Hostname")
	}
	if lo.DNS != "" {
		badOpt = append(badOpt, "DNS")
	}

	if lo.AddCaps != "" {
		badOpt = append(badOpt, "AddCaps")
	}
	if lo.DropCaps != "" {
		badOpt = append(badOpt, "DropCaps")
	}
	if lo.AllowSUID {
		badOpt = append(badOpt, "AllowSUID")
	}
	if lo.KeepPrivs {
		badOpt = append(badOpt, "KeepPrivs")
	}
	if lo.NoPrivs {
		badOpt = append(badOpt, "NoPrivs")
	}
	if len(lo.SecurityOpts) > 0 {
		badOpt = append(badOpt, "SecurityOpts")
	}
	if lo.NoUmask {
		badOpt = append(badOpt, "NoUmask")
	}

	if lo.CGroupsJSON != "" {
		badOpt = append(badOpt, "CGroupsJSON")
	}

	// ConfigFile always set by CLI. We should support only the default from build time.
	if lo.ConfigFile != "" && lo.ConfigFile != buildcfg.APPTAINER_CONF_FILE {
		badOpt = append(badOpt, "ConfigFile")
	}

	if lo.ShellPath != "" {
		badOpt = append(badOpt, "ShellPath")
	}
	if lo.PwdPath != "" {
		badOpt = append(badOpt, "PwdPath")
	}

	if lo.Boot {
		badOpt = append(badOpt, "Boot")
	}
	if lo.NoInit {
		badOpt = append(badOpt, "NoInit")
	}
	if lo.Contain {
		badOpt = append(badOpt, "Contain")
	}
	if lo.ContainAll {
		badOpt = append(badOpt, "ContainAll")
	}

	if lo.AppName != "" {
		badOpt = append(badOpt, "AppName")
	}

	if lo.KeyInfo != nil {
		badOpt = append(badOpt, "KeyInfo")
	}

	if lo.SIFFUSE {
		badOpt = append(badOpt, "SIFFUSE")
	}
	if lo.CacheDisabled {
		badOpt = append(badOpt, "CacheDisabled")
	}

	if len(badOpt) > 0 {
		return fmt.Errorf("%w: %s", ErrUnsupportedOption, strings.Join(badOpt, ","))
	}

	return nil
}

// createSpec produces an OCI runtime specification, suitable to launch a
// container. This spec excludes the Process config, as this have to be
// computed where the image config is available, to account for the image's CMD
// / ENTRYPOINT / ENV.
func (l *Launcher) createSpec() (*specs.Spec, error) {
	spec := minimalSpec()

	// If we are *not* requesting fakeroot, then we need to map the container
	// uid back to host uid, through the initial fakeroot userns.
	if !l.cfg.Fakeroot && os.Getuid() != 0 {
		uidMap, gidMap, err := l.getReverseUserMaps()
		if err != nil {
			return nil, err
		}
		spec.Linux.UIDMappings = uidMap
		spec.Linux.GIDMappings = gidMap
	}

	spec = addNamespaces(spec, l.cfg.Namespaces)

	cwd, err := l.getProcessCwd()
	if err != nil {
		return nil, err
	}
	spec.Process.Cwd = cwd

	mounts, err := l.getMounts()
	if err != nil {
		return nil, err
	}
	spec.Mounts = mounts

	return &spec, nil
}

func (l *Launcher) updatePasswdGroup(rootfs string) error {
	uid := os.Getuid()
	gid := os.Getgid()

	if os.Getuid() == 0 || l.cfg.Fakeroot {
		return nil
	}

	containerPasswd := filepath.Join(rootfs, "etc", "passwd")
	containerGroup := filepath.Join(rootfs, "etc", "group")

	pw, err := user.CurrentOriginal()
	if err != nil {
		return err
	}

	sylog.Debugf("Updating passwd file: %s", containerPasswd)
	content, err := files.Passwd(containerPasswd, pw.Dir, uid)
	if err != nil {
		return fmt.Errorf("while creating passwd file: %w", err)
	}
	if err := os.WriteFile(containerPasswd, content, 0o755); err != nil {
		return fmt.Errorf("while writing passwd file: %w", err)
	}

	sylog.Debugf("Updating group file: %s", containerGroup)
	content, err = files.Group(containerGroup, uid, []int{gid})
	if err != nil {
		return fmt.Errorf("while creating group file: %w", err)
	}
	if err := os.WriteFile(containerGroup, content, 0o755); err != nil {
		return fmt.Errorf("while writing passwd file: %w", err)
	}

	return nil
}

// Exec will interactively execute a container via the runc low-level runtime.
// image is a reference to an OCI image, e.g. docker://ubuntu or oci:/tmp/mycontainer
func (l *Launcher) Exec(ctx context.Context, image string, process string, args []string, instanceName string) error {
	if instanceName != "" {
		return fmt.Errorf("%w: instanceName", ErrNotImplemented)
	}

	bundleDir, err := os.MkdirTemp("", "oci-bundle")
	if err != nil {
		return nil
	}
	defer func() {
		sylog.Debugf("Removing OCI bundle at: %s", bundleDir)
		if err := os.RemoveAll(bundleDir); err != nil {
			sylog.Errorf("Couldn't remove OCI bundle %s: %v", bundleDir, err)
		}
	}()

	sylog.Debugf("Creating OCI bundle at: %s", bundleDir)

	// TODO - propagate auth config
	sysCtx := &types.SystemContext{
		// OCIInsecureSkipTLSVerify: cp.b.Opts.NoHTTPS,
		// DockerAuthConfig:         cp.b.Opts.DockerAuthConfig,
		// DockerDaemonHost:         cp.b.Opts.DockerDaemonHost,
		OSChoice:                "linux",
		AuthFilePath:            syfs.DockerConf(),
		DockerRegistryUserAgent: useragent.Value(),
	}
	// if cp.b.Opts.NoHTTPS {
	//      cp.sysCtx.DockerInsecureSkipTLSVerify = types.NewOptionalBool(true)
	// }

	var imgCache *cache.Handle
	if !l.cfg.CacheDisabled {
		imgCache, err = cache.New(cache.Config{
			ParentDir: os.Getenv(cache.DirEnv),
		})
		if err != nil {
			return err
		}
	}

	// Create OCI runtime spec, excluding the Process settings which must consider the image spec.
	spec, err := l.createSpec()
	if err != nil {
		return fmt.Errorf("while creating OCI spec: %w", err)
	}

	// Create a bundle - obtain and extract the image.
	b, err := native.New(
		native.OptBundlePath(bundleDir),
		native.OptImageRef(image),
		native.OptSysCtx(sysCtx),
		native.OptImgCache(imgCache),
	)
	if err != nil {
		return err
	}
	if err := b.Create(ctx, spec); err != nil {
		return err
	}

	// With reference to the bundle's image spec, now set the process configuration.
	imgSpec := b.ImageSpec()
	if imgSpec == nil {
		return fmt.Errorf("bundle has no image spec")
	}
	specProcess, err := l.getProcess(ctx, *imgSpec, image, b.Path(), process, args)
	if err != nil {
		return err
	}
	spec.Process = specProcess
	b.Update(ctx, spec)

	if err := l.updatePasswdGroup(tools.RootFs(b.Path()).Path()); err != nil {
		return err
	}

	id, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("while generating container id: %w", err)
	}

	if os.Getuid() == 0 {
		// Direct execution of runc/crun run.
		err = Run(ctx, id.String(), b.Path(), "")
	} else {
		// Reexec apptainer oci run in a userns with mappings.
		err = RunNS(ctx, id.String(), b.Path(), "")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	return err
}

func mergeMap(a map[string]string, b map[string]string) map[string]string {
	for k, v := range b {
		a[k] = v
	}
	return a
}
